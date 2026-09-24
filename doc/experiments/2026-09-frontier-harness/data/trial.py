"""Run one Terminal-Bench 2 task against Strument without Docker.

The task image's rootfs (pull.py) is the lower layer of an overlay, so each
trial starts from the same files. The agent runs chrooted in its own network
namespace, whose only way out is a SOCKS5 forwarder to an allowlisting proxy
on the host: openrouter.ai:443 and nothing else. The task's own tools have no
route at all, which is what allow_internet = false asks for. The verifier runs
afterwards on the host network, because test.sh installs curl and uv.

Usage: trial.py TASK MODEL_ALIAS OUT_DIR   (key from OPENROUTER_API_KEY)
"""
import json, os, select, shutil, signal, socket, struct, subprocess, sys, threading, time, tomllib

TB2 = "/tmp/harness/tb2"
IMAGES = "/tmp/fh/images"
STRUMENT = os.environ.get("STRUMENT", "/tmp/fh/strument")
ALLOWED = {("openrouter.ai", 443)}

# The registries FrontierHarness's trial egress policy allowed (providers.sh at
# e837a70): agents could install packages, and a task image without git or a
# compiler expects that. The model's own API goes through the SOCKS door above;
# these go through the HTTP proxy the task's tools are pointed at.
REGISTRIES = ["astral.sh", "*.astral.sh", "github.com", "*.github.com", "*.githubusercontent.com",
              "*.supabase.co", "pypi.org", "*.pythonhosted.org", "*.npmjs.org", "*.ubuntu.com",
              "*.debian.org", "*.pytorch.org", "*.ecr.aws", "*.cloudfront.net"]


def registry_allowed(host, port):
    import fnmatch
    return port in (80, 443) and any(fnmatch.fnmatch(host.lower(), p) for p in REGISTRIES)


def http_proxy(path, log):
    """An HTTP proxy on a Unix socket: CONNECT and absolute-URI requests to the
    registries, and nothing else."""
    if os.path.exists(path):
        os.remove(path)
    srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    srv.bind(path)
    srv.listen(64)

    def pump(a, b):
        try:
            while True:
                r, _, _ = select.select([a, b], [], [], 600)
                if not r:
                    return
                for x in r:
                    d = x.recv(65536)
                    if not d:
                        return
                    (b if x is a else a).sendall(d)
        except OSError:
            pass
        finally:
            a.close()
            b.close()

    def handle(c):
        try:
            head = b""
            while b"\r\n\r\n" not in head:
                chunk = c.recv(65536)
                if not chunk:
                    c.close()
                    return
                head += chunk
            line = head.split(b"\r\n", 1)[0].decode("latin-1")
            method, target, _ = line.split(" ", 2)
            if method == "CONNECT":
                host, port = target.rsplit(":", 1)
                port = int(port)
            else:
                from urllib.parse import urlsplit
                u = urlsplit(target)
                host, port = u.hostname or "", u.port or 80
            ok = registry_allowed(host, port)
            log.write(f"{time.time():.0f} {'ALLOW' if ok else 'DENY'} http {host}:{port}\n")
            log.flush()
            if not ok:
                c.sendall(b"HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
                c.close()
                return
            up = socket.create_connection((host, port), timeout=30)
            if method == "CONNECT":
                c.sendall(b"HTTP/1.1 200 Connection Established\r\n\r\n")
                rest = head.split(b"\r\n\r\n", 1)[1]
                if rest:
                    up.sendall(rest)
            else:
                from urllib.parse import urlsplit
                u = urlsplit(target)
                pathq = (u.path or "/") + (("?" + u.query) if u.query else "")
                up.sendall(head.replace(target.encode("latin-1"), pathq.encode("latin-1"), 1))
            pump(c, up)
        except (OSError, ValueError):
            c.close()

    def serve():
        while True:
            c, _ = srv.accept()
            threading.Thread(target=handle, args=(c,), daemon=True).start()
    threading.Thread(target=serve, daemon=True).start()


def socks_server(path, log):
    """A SOCKS5 server on a Unix socket: no auth, CONNECT only, allowlisted."""
    if os.path.exists(path):
        os.remove(path)
    srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    srv.bind(path)
    srv.listen(64)

    def pump(a, b):
        try:
            while True:
                r, _, _ = select.select([a, b], [], [], 600)
                if not r:
                    return
                for s in r:
                    data = s.recv(65536)
                    if not data:
                        return
                    (b if s is a else a).sendall(data)
        except OSError:
            pass
        finally:
            a.close()
            b.close()

    def handle(c):
        try:
            c.recv(262)                                   # greeting
            c.sendall(b"\x05\x00")                        # no auth
            hdr = c.recv(4)
            atyp = hdr[3]
            if atyp == 3:
                host = c.recv(c.recv(1)[0]).decode()
            elif atyp == 1:
                host = socket.inet_ntoa(c.recv(4))
            else:
                host = "?"
                c.recv(16)
            port = struct.unpack(">H", c.recv(2))[0]
            ok = hdr[1] == 1 and (host, port) in ALLOWED
            log.write(f"{time.time():.0f} {'ALLOW' if ok else 'DENY'} {host}:{port}\n")
            log.flush()
            if not ok:
                c.sendall(b"\x05\x02\x00\x01\x00\x00\x00\x00\x00\x00")
                c.close()
                return
            up = socket.create_connection((host, port), timeout=30)
            c.sendall(b"\x05\x00\x00\x01\x00\x00\x00\x00\x00\x00")
            pump(c, up)
        except OSError:
            c.close()

    def serve():
        while True:
            c, _ = srv.accept()
            threading.Thread(target=handle, args=(c,), daemon=True).start()
    threading.Thread(target=serve, daemon=True).start()


BRIDGE = r'''
import fcntl, socket, struct, sys, threading, select
# Bring loopback up in the fresh namespace: SIOCSIFFLAGS, IFF_UP|IFF_RUNNING.
s = socket.socket(); fcntl.ioctl(s, 0x8914, struct.pack("16sh14x", b"lo", 0x1 | 0x40)); s.close()
def listen(port, target):
    srv = socket.socket(); srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind(("127.0.0.1", port)); srv.listen(64)
    def accept():
        while True:
            c, _ = srv.accept()
            u = socket.socket(socket.AF_UNIX); u.connect(target)
            threading.Thread(target=pump, args=(c, u), daemon=True).start()
    threading.Thread(target=accept, daemon=True).start()
def pump(a, b):
    try:
        while True:
            r, _, _ = select.select([a, b], [], [], 600)
            if not r: return
            for x in r:
                d = x.recv(65536)
                if not d: return
                (b if x is a else a).sendall(d)
    except OSError: pass
    finally: a.close(); b.close()
listen(1080, sys.argv[1])
if len(sys.argv) > 2:
    listen(3128, sys.argv[2])
print("ready", flush=True)
threading.Event().wait()
'''

CONFIG = '''openrouter = provider("openrouter", api_key=env("OPENROUTER_API_KEY"), proxy="socks5h://127.0.0.1:1080")
models = {{
    "mimo": model(openrouter, "xiaomi/mimo-v2.6-flash", context = 200000, cache = True),
    "k3": model(openrouter, "moonshotai/kimi-k3", context = 1048576, cache = True,
                extra_params = {{"provider": {{"order": {order}, "allow_fallbacks": False}}}}),
}}
default = "{model}"
sandbox = ""
'''


def trust_ca(lower, upper):
    """This cloud environment intercepts TLS, as Runta's egress does; like
    FrontierHarness's CA overlay, the task image is made to trust the
    interceptor, through files rather than environment variables (the model's
    commands get Strument's filtered environment). A no-op where there is no
    such CA."""
    ccr = os.environ.get("CCR_CA", "/root/.ccr/ca-bundle.crt")
    if not os.path.exists(ccr):
        return
    extra = open(ccr).read()
    rel = "etc/ssl/certs/ca-certificates.crt"
    base = open(os.path.join(lower, rel)).read() if os.path.exists(os.path.join(lower, rel)) else ""
    os.makedirs(os.path.join(upper, "etc/ssl/certs"), exist_ok=True)
    with open(os.path.join(upper, rel), "w") as f:
        f.write(base + "\n" + extra)
    with open(os.path.join(upper, "etc/pip.conf"), "w") as f:
        f.write("[global]\ncert = /etc/ssl/certs/ca-certificates.crt\n")
    os.makedirs(os.path.join(upper, "root/.config/uv"), exist_ok=True)
    with open(os.path.join(upper, "root/.config/uv/uv.toml"), "w") as f:
        f.write("native-tls = true\n")


def run(task, model, out):
    out = os.path.abspath(out)
    os.makedirs(out, exist_ok=True)
    tdir = os.path.join(TB2, task)
    meta = tomllib.load(open(os.path.join(tdir, "task.toml"), "rb"))
    lower = os.path.join(IMAGES, task)
    cfg = json.load(open(os.path.join(lower, ".image-config.json")))["config"]
    workdir = meta["environment"].get("workdir") or cfg.get("WorkingDir") or "/app"
    image_env = dict(e.split("=", 1) for e in cfg.get("Env") or [])
    image_env.update(meta["environment"].get("env") or {})
    agent_timeout = int(meta["agent"]["timeout_sec"])

    upper, work, merged = (os.path.join(out, d) for d in ("upper", "work", "merged"))
    for d in (upper, work, merged):
        os.makedirs(d, exist_ok=True)

    # Staged into the upper layer, so the chroot sees them without touching the image.
    fh = os.path.join(upper, "fh")
    os.makedirs(os.path.join(fh, "cfg", "strument"), exist_ok=True)
    shutil.copy(STRUMENT, os.path.join(fh, "strument"))
    shutil.copy("/etc/ssl/certs/ca-certificates.crt", os.path.join(fh, "ca.crt"))
    with open(os.path.join(fh, "cfg", "strument", "config.star"), "w") as f:
        f.write(CONFIG.format(model=model, order=os.environ.get("FH_PROVIDERS", '["fireworks"]')))
    with open(os.path.join(fh, "instruction.md"), "w") as f:
        f.write(open(os.path.join(tdir, "instruction.md")).read())

    sock = os.path.join(out, "socks.sock")
    netlog = open(os.path.join(out, "egress.log"), "w")
    socks_server(sock, netlog)
    hsock = os.path.join(out, "http.sock")
    if os.environ.get("FH_REGISTRIES", "1") == "1":
        http_proxy(hsock, netlog)
    else:
        hsock = None

    holder = subprocess.Popen(["unshare", "--net", "sleep", "infinity"])
    time.sleep(0.2)
    netns = f"/proc/{holder.pid}/ns/net"
    bridge = subprocess.Popen(["nsenter", f"--net={netns}", sys.executable, "-c", BRIDGE, sock]
                              + ([hsock] if hsock else []),
                              stdout=subprocess.PIPE, text=True)
    assert bridge.stdout.readline().strip() == "ready"

    env = {"PATH": image_env.get("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"),
           "HOME": "/root", "TERM": "dumb", "SSL_CERT_FILE": "/fh/ca.crt",
           "XDG_CONFIG_HOME": "/fh/cfg", "XDG_STATE_HOME": "/fh/state",
           "OPENROUTER_API_KEY": os.environ["OPENROUTER_API_KEY"]}
    env.update({k: v for k, v in image_env.items() if k != "PATH"})
    if hsock:
        for k in ("http_proxy", "https_proxy", "HTTP_PROXY", "HTTPS_PROXY"):
            env[k] = "http://127.0.0.1:3128"
        env["no_proxy"] = env["NO_PROXY"] = "localhost,127.0.0.1"
        trust_ca(lower, upper)
    inner = ("mount -t proc proc {m}/proc && mount --rbind /dev {m}/dev && "
             "exec chroot {m} /bin/sh -c 'cd {w} && exec /fh/strument --no-git --yes all "
             "-m \"$(cat /fh/instruction.md)\" </dev/null'").format(m=merged, w=workdir)
    mount = f"mount -t overlay overlay -o lowerdir={lower},upperdir={upper},workdir={work} {merged} && "
    t0 = time.time()
    with open(os.path.join(out, "agent.out"), "w") as so:
        agent = subprocess.Popen(["nsenter", f"--net={netns}", "unshare", "--mount", "--pid", "--fork",
                                  "--kill-child", "sh", "-c", mount + inner],
                                 env=env, stdout=so, stderr=subprocess.STDOUT, start_new_session=True)
        try:
            agent.wait(timeout=agent_timeout)
            agent_status = agent.returncode
        except subprocess.TimeoutExpired:
            os.killpg(agent.pid, signal.SIGKILL)
            agent.wait()
            agent_status = "timeout"
    agent_seconds = time.time() - t0
    bridge.kill()
    holder.kill()

    # The verifier: the task's own tests, on the host network, in a fresh mount
    # namespace over the same upper layer the agent left behind.
    shutil.copytree(os.path.join(tdir, "tests"), os.path.join(upper, "tests"), dirs_exist_ok=True)
    os.makedirs(os.path.join(upper, "logs", "verifier"), exist_ok=True)
    shutil.copy("/etc/resolv.conf", os.path.join(upper, "etc", "resolv.conf")) if os.path.isdir(os.path.join(upper, "etc")) else (
        os.makedirs(os.path.join(upper, "etc"), exist_ok=True), shutil.copy("/etc/resolv.conf", os.path.join(upper, "etc", "resolv.conf")))
    # This cloud environment intercepts TLS, and a task image does not trust the
    # interceptor's CA: the verifier gets the environment's bundle. Elsewhere
    # CCR_CA is simply absent and the image's own trust store is used.
    ccr = os.environ.get("CCR_CA", "/root/.ccr/ca-bundle.crt")
    venv = {"PATH": env["PATH"], "HOME": "/root", "DEBIAN_FRONTEND": "noninteractive"}
    if os.path.exists(ccr):
        shutil.copy(ccr, os.path.join(upper, "fh", "verifier-ca.crt"))
        venv.update({"SSL_CERT_FILE": "/fh/verifier-ca.crt", "CURL_CA_BUNDLE": "/fh/verifier-ca.crt",
                     "REQUESTS_CA_BUNDLE": "/fh/verifier-ca.crt"})
    venv.update({k: v for k, v in image_env.items() if k != "PATH"})
    vinner = ("mount -t proc proc {m}/proc && mount --rbind /dev {m}/dev && "
              "exec chroot {m} /bin/sh -c 'cd {w} && bash /tests/test.sh'").format(m=merged, w=workdir)
    with open(os.path.join(out, "verifier.out"), "w") as vo:
        try:
            subprocess.run(["unshare", "--mount", "--pid", "--fork", "--kill-child", "sh", "-c", mount + vinner],
                           env=venv, stdout=vo, stderr=subprocess.STDOUT,
                           timeout=int(meta["verifier"]["timeout_sec"]))
        except subprocess.TimeoutExpired:
            pass
    reward_path = os.path.join(upper, "logs", "verifier", "reward.txt")
    reward = open(reward_path).read().strip() if os.path.exists(reward_path) else None

    result = {"task": task, "model": model, "agent_status": agent_status,
              "agent_seconds": round(agent_seconds, 1), "reward": reward}
    json.dump(result, open(os.path.join(out, "result.json"), "w"), indent=1)
    print(json.dumps(result))


if __name__ == "__main__":
    run(*sys.argv[1:4])
