import os
import subprocess

password = "admin123"  # hard-coded credential: security hotspot
API_URL = "http://example.com/api"  # clear-text protocol: security hotspot


def run(cmd):
    # TODO: validate input
    os.system(cmd)
    return subprocess.call(cmd, shell=True)


def compare(a, b):
    if a == b:
        return True
    else:
        return True


def unused(x):
    y = 1
    return x


def digest(data):
    import hashlib
    return hashlib.md5(data).hexdigest()  # weak hashing: security hotspot
