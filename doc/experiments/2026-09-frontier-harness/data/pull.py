"""Pull a Docker Hub image into a rootfs directory, without a Docker daemon.

Resolves a multi-arch index to linux/amd64, applies layers in order, and
honours whiteouts (.wh.name deletes name; .wh..wh..opq empties the directory).
"""
import io, json, os, shutil, sys, tarfile, urllib.request

def get(url, token=None, accept=None):
    req = urllib.request.Request(url)
    if token:
        req.add_header("Authorization", "Bearer " + token)
    if accept:
        req.add_header("Accept", accept)
    return urllib.request.urlopen(req, timeout=300)

def pull(image, dest):
    name, tag = image.rsplit(":", 1)
    if "/" not in name:
        name = "library/" + name
    token = json.load(get(f"https://auth.docker.io/token?service=registry.docker.io&scope=repository:{name}:pull"))["token"]
    base = f"https://registry-1.docker.io/v2/{name}"
    accept = ",".join([
        "application/vnd.oci.image.index.v1+json",
        "application/vnd.docker.distribution.manifest.list.v2+json",
        "application/vnd.oci.image.manifest.v1+json",
        "application/vnd.docker.distribution.manifest.v2+json",
    ])
    man = json.load(get(f"{base}/manifests/{tag}", token, accept))
    if "manifests" in man:
        pick = [m for m in man["manifests"] if m.get("platform", {}).get("architecture") == "amd64"
                and m.get("platform", {}).get("os") == "linux"][0]
        man = json.load(get(f"{base}/manifests/{pick['digest']}", token, accept))
    config = json.load(get(f"{base}/blobs/{man['config']['digest']}", token))
    os.makedirs(dest, exist_ok=True)
    for layer in man["layers"]:
        data = get(f"{base}/blobs/{layer['digest']}", token).read()
        with tarfile.open(fileobj=io.BytesIO(data), mode="r:*") as tf:
            for m in tf.getmembers():
                d, b = os.path.split(m.name)
                if b == ".wh..wh..opq":
                    target = os.path.join(dest, d)
                    for e in os.listdir(target) if os.path.isdir(target) else []:
                        p = os.path.join(target, e)
                        shutil.rmtree(p) if os.path.isdir(p) and not os.path.islink(p) else os.remove(p)
                    continue
                if b.startswith(".wh."):
                    p = os.path.join(dest, d, b[4:])
                    if os.path.islink(p) or os.path.isfile(p):
                        os.remove(p)
                    elif os.path.isdir(p):
                        shutil.rmtree(p)
                    continue
                p = os.path.join(dest, m.name)
                if os.path.lexists(p) and not (m.isdir() and os.path.isdir(p)):
                    shutil.rmtree(p) if os.path.isdir(p) and not os.path.islink(p) else os.remove(p)
                tf.extract(m, dest, numeric_owner=True, filter="fully_trusted")
        print(f"  layer {layer['digest'][7:19]} {layer['size'] / 1e6:.1f} MB", flush=True)
    with open(os.path.join(dest, ".image-config.json"), "w") as f:
        json.dump(config, f)

if __name__ == "__main__":
    pull(sys.argv[1], sys.argv[2])
