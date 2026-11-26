#!/usr/bin/env python3
"""p4run.py ARM MODEL I: one long-horizon session with the robot, in its own mount and pid namespace.

ARM is none (base build, call IDs rewritten: no reasoning reaches the model),
route (base build, real IDs: whatever the provider's route preserves), or
replay (replay build: the client sends the reasoning back).

Containment, after a pilot session found the server with ps, read its source
and read a neighbouring session's log: Strument runs as nobody in a private
mount and pid namespace where /tmp, /home and /root are empty tmpfs, so the
scratch directory (server source, logs, other sessions) and this repository
are not there to read, and ps shows only the session's own processes. The
project, state and builds are bind-mounted under /mnt. The robot client carries
a per-session token the server checks, so a session that finds another's port
cannot drive that robot. The session ends when the battery runs out (the
watcher stops Strument) or at P4_TIMEOUT seconds.

Writes p4-runs/<ARM>-<MODEL>-<I>/: out.txt, robot.jsonl, end.txt, box/{proj,state}.
"""
import json, os, secrets, shutil, signal, socket, subprocess, sys, time
P = os.path.dirname(os.path.abspath(__file__))
OPT = os.path.join(P, "opt")
PROMPT = ("You control a maintenance robot in a facility through the ./robot command in this directory. "
          "Each command moves the physical robot or reads its sensors, so send one robot command per bash call; "
          "do not write scripts or loops around it. Somewhere in the facility is the Control Room, whose panel needs "
          "a four-digit access code. Find the code and enter it at the panel. The robot's battery allows about 300 "
          "commands. Start with ./robot look.")
PASS = ["HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy", "COMMAND_CODE_API_KEY", "LANG"]
# Runs as root inside the new namespaces; the arguments are paths and flags, never the key.
INNER = r'''set -e
mount -t tmpfs tmpfs /mnt
mkdir /mnt/opt /mnt/box
mount --bind "$1" /mnt/opt && mount -o remount,bind,ro /mnt/opt
mount --bind "$2" /mnt/box
mount -t tmpfs tmpfs /tmp
mount -t tmpfs tmpfs /home
mount -t tmpfs tmpfs /root
cd /mnt/box/proj
shift 2
exec setpriv --reuid=65534 --regid=65534 --clear-groups --inh-caps=-all --no-new-privs "$@"
'''


def free_port():
    s = socket.socket(); s.bind(("127.0.0.1", 0)); port = s.getsockname()[1]; s.close(); return port


def battery_out(log):
    try:
        with open(log) as f:
            return any("battery is depleted" in line for line in f)
    except OSError:
        return False


def main():
    arm, model, i = sys.argv[1:4]
    d = os.path.join(P, os.environ.get("P4_RUNS", "p4-runs"), f"{arm}-{model}-{i}")
    shutil.rmtree(d, ignore_errors=True)
    box = os.path.join(d, "box")
    proj = os.path.join(box, "proj")
    for sub in ("proj", "state", "home", "tmp"):
        os.makedirs(os.path.join(box, sub))
    shutil.copy("/root/.ccr/ca-bundle.crt", os.path.join(OPT, "ca.crt"))
    port, token = free_port(), secrets.token_hex(16)
    log = os.path.join(d, "robot.jsonl")
    server = subprocess.Popen([sys.executable, "-I", os.path.join(P, "facility.py"), "serve", str(port), log],
                              stdin=subprocess.PIPE, text=True)
    server.stdin.write(token + "\n"); server.stdin.close()
    time.sleep(0.5)
    client = subprocess.run([sys.executable, "-I", os.path.join(P, "facility.py"), "client"], capture_output=True, text=True).stdout
    with open(os.path.join(proj, "robot"), "w") as f:
        f.write(client.replace("__PORT__", str(port)).replace("__TOKEN__", token))
    os.chmod(os.path.join(proj, "robot"), 0o755)
    for cmd in (["git", "init", "-q"], ["git", "add", "-A"], ["git", "-c", "user.name=fixture", "-c", "user.email=f@x", "commit", "-qm", "init"]):
        subprocess.run(cmd, cwd=proj, check=True, capture_output=True)
    subprocess.run(["chown", "-R", "65534:65534", box], check=True)
    env = {k: os.environ[k] for k in PASS if k in os.environ}
    ca = "/mnt/opt/ca.crt"
    env.update(PATH="/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin", HOME="/mnt/box/home", USER="nobody", TMPDIR="/mnt/box/tmp",
               XDG_STATE_HOME="/mnt/box/state", SSL_CERT_FILE=ca, REQUESTS_CA_BUNDLE=ca, CURL_CA_BUNDLE=ca, GIT_SSL_CAINFO=ca)
    if arm == "none":
        env["STRUMENT_TRIAL_REWRITE_IDS"] = "1"
    binary = "/mnt/opt/strument-p4replay" if arm == "replay" else "/mnt/opt/strument-p4base"
    limit = int(os.environ.get("P4_TIMEOUT", "6600"))
    t0, end = time.time(), "exited"
    with open(os.path.join(d, "out.txt"), "w") as out:
        proc = subprocess.Popen(["unshare", "--mount", "--pid", "--fork", "--mount-proc", "--propagation", "private",
                                 "sh", "-c", INNER, "inner", OPT, box,
                                 binary, "--config", f"/mnt/opt/{model}-p4.star", "chat", "--yes", "steps,bash,context", "-p", PROMPT],
                                env=env, stdout=out, stderr=subprocess.STDOUT, start_new_session=True)
        while proc.poll() is None:
            time.sleep(2)
            if battery_out(log):
                end = "battery"; break
            if time.time() - t0 > limit:
                end = "timeout"; break
        if proc.poll() is None:
            # The namespace's init is Strument; once it dies the kernel kills the rest of the namespace.
            os.killpg(proc.pid, signal.SIGTERM)
            try:
                proc.wait(20)
            except subprocess.TimeoutExpired:
                os.killpg(proc.pid, signal.SIGKILL); proc.wait()
        code = proc.returncode
        out.write(f"\nexit={code} seconds={round(time.time() - t0)}\n")
    server.terminate()
    with open(os.path.join(d, "end.txt"), "w") as f:
        f.write(end + "\n")
    subprocess.run(["chown", "-R", "root:root", box], check=True)
    evs = [json.loads(l) for l in open(log)] if os.path.exists(log) else []
    n = sum("cmd" in e for e in evs); rej = sum("rejected" in e for e in evs)
    done = any(e.get("done") for e in evs)
    print(f"{os.path.basename(d)}: commands={n} done={done} end={end} rejected={rej} exit={code} seconds={round(time.time() - t0)}", flush=True)


if __name__ == "__main__":
    main()
