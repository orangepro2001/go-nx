#!/usr/bin/env python3
"""End to end check of the real nx-host and nx-probe programs, as separate processes.

    scripts/e2e-host-probe.py --host ./nx-host --probe ./nx-probe

Starts nx-host in test mode (a synthetic screen and an injector that only logs, so
it never touches the desktop of the machine it runs on) on a loopback port the
system chooses, talks to it the way an application would (JSON Lines on its stdin
and stdout), pairs nx-probe with it, and checks what each side reports. It proves
the protocol path and the sidecar interface, not real screen capture or input.
"""
import argparse
import json
import os
import subprocess
import sys
import tempfile
import threading
import queue
import shlex

failures = []


def ok(msg):
    print("ok  ", msg, flush=True)


def check(cond, msg):
    if cond:
        ok(msg)
    else:
        failures.append(msg)
        print("FAIL", msg, flush=True)
    return cond


class Host:
    def __init__(self, exe, state_dir):
        self.stderr = open(os.path.join(state_dir, "host.stderr"), "w+")
        self.p = subprocess.Popen(
            [*exe, "-test-mode", "-listen", "127.0.0.1:0", "-size", "640x480", "-fps", "20",
             "-state-dir", os.path.join(state_dir, "host")],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.stderr, text=True, encoding="utf-8")
        self.lines = queue.Queue()
        self.raw = []
        threading.Thread(target=self._read, daemon=True).start()

    def _read(self):
        for line in self.p.stdout:
            self.raw.append(line)
            self.lines.put(line)
        self.lines.put(None)

    def send(self, obj):
        self.p.stdin.write(json.dumps(obj) + "\n")
        self.p.stdin.flush()

    def event(self, want, timeout=20):
        """The next event named want; the ones before it are skipped."""
        while True:
            try:
                line = self.lines.get(timeout=timeout)
            except queue.Empty:
                raise SystemExit("FAIL timed out waiting for the %r event" % want)
            if line is None:
                raise SystemExit("FAIL nx-host's output ended while waiting for %r" % want)
            ev = json.loads(line)
            if ev.get("event") == want:
                return ev


def run_probe(exe, *args):
    r = subprocess.run([*exe, *args, "-json", "-timeout", "30s"], capture_output=True, text=True, encoding="utf-8", timeout=60)
    try:
        res = json.loads(r.stdout.strip().splitlines()[-1]) if r.stdout.strip() else {}
    except json.JSONDecodeError:
        res = {}
    return r.returncode, res, r.stderr


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--host", required=True, help="the nx-host program (a command line is fine)")
    ap.add_argument("--probe", required=True, help="the nx-probe program (a command line is fine)")
    a = ap.parse_args()

    with tempfile.TemporaryDirectory(ignore_cleanup_errors=True) as tmp:
        # --host and --probe may be a command with arguments (e.g. "wine nx-host.exe").
        posix = os.name != "nt"  # backslashes in a Windows path are not escapes
        host_cmd, probe_cmd = shlex.split(a.host, posix=posix), shlex.split(a.probe, posix=posix)
        h = Host(host_cmd, tmp)
        try:
            ready = h.event("ready")
            check(ready.get("schema") == 1, "ready carries schema 1")
            check(ready.get("testMode") is True and ready.get("source") == "pattern" and ready.get("inputMode") == "log",
                  "test mode is synthetic screen + logged input")
            listen = ready.get("listen", "")
            check(listen.startswith("127.0.0.1:") and not listen.endswith(":0"), "listening on the port the system chose (%s)" % listen)
            ident = ready.get("identity", "")
            check(len(ident) == 64, "ready names the host identity")
            check(ready.get("requireAuth") is True, "a host starts in strict mode")

            h.send({"id": "p", "cmd": "pair", "seconds": 60})
            pairing = h.event("pairing")
            check(pairing.get("id") == "p" and len(pairing.get("code", "")) == 6, "pairing opens with a six digit code and echoes the id")
            code = pairing["code"]

            probe_state = os.path.join(tmp, "probe-state")
            common = ["-addr", listen, "-fingerprint", ident, "-state-dir", probe_state]
            rc, res, err = run_probe(probe_cmd, *common, "-code", code, "-input", "-markers")
            check(rc == 0, "nx-probe passes with the pairing code (exit %d) %s" % (rc, err.strip()))
            check(res.get("schema") == 1 and res.get("protocol") == 1 and res.get("connected") is True, "the probe speaks protocol 1")
            check(res.get("frames", 0) >= 3 and res.get("markersOk") is True, "pictures arrive intact (markers ok)")
            check(res.get("inputsSent") == 8 and res.get("receipts") == 8 and res.get("receiptFailures") == 0,
                  "all 8 inputs are acknowledged")
            conn = h.event("connected")
            check(len(conn.get("identity", "")) == 64 and conn.get("protocol") == 1, "the host reports the connection")
            h.event("disconnected")

            rc, res, err = run_probe(probe_cmd, *common, "-input", "-v0")
            check(rc == 0 and res.get("protocol") == 0 and res.get("receipts") == res.get("inputsSent") == 8,
                  "the paired device comes back with no code, over the original protocol (exit %d) %s" % (rc, err.strip()))
            h.event("disconnected")

            rc, res, _ = run_probe(probe_cmd, "-addr", listen, "-fingerprint", "ab" * 32, "-code", code)
            check(rc == 1 and res.get("connected") is False, "a wrong fingerprint is refused before anything is sent")

            rc, res, _ = run_probe(probe_cmd, "-addr", listen, "-fingerprint", ident, "-code", "000000",
                                   "-state-dir", os.path.join(tmp, "other-probe"))
            check(rc == 1, "an unpaired device with a wrong code is refused")
            denied = h.event("denied")
            check(denied.get("reason") in ("bad-code", "untrusted"), "the host reports why it refused (%s)" % denied.get("reason"))

            h.send({"id": "s", "cmd": "status"})
            st = h.event("status")
            check(st.get("id") == "s" and len(st.get("trusted", [])) == 1, "status lists the one paired device")
            check(st.get("frames", 0) > 0 and st.get("inputs", 0) >= 16, "status counts pictures and inputs")

            h.send({"id": "x", "cmd": "stop"})
            h.event("ack")
            stopped = h.event("stopped")
            check(stopped.get("reason") == "stop", "stop is acknowledged and reported")
            try:
                rc = h.p.wait(timeout=20)
            except subprocess.TimeoutExpired:
                rc = None
            check(rc == 0, "nx-host exits 0 (%s)" % rc)
            bad = []
            for line in h.raw:
                try:
                    ev = json.loads(line)
                    if not isinstance(ev, dict) or "event" not in ev:
                        bad.append(line)
                except json.JSONDecodeError:
                    bad.append(line)
            check(not bad, "every line nx-host wrote to stdout is one JSON event %s" % bad[:2])
        finally:
            if h.p.poll() is None:
                h.p.kill()
            h.stderr.seek(0)
            sys.stdout.flush()
            if failures:
                print("---- nx-host stderr ----")
                print(h.stderr.read())
            h.stderr.close()  # Windows cannot delete a directory with an open file in it

    if failures:
        print("\n%d check(s) failed" % len(failures))
        return 1
    print("\nall checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
