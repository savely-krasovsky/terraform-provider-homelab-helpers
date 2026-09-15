#!/usr/bin/env python3
# Copyright Savely Krasovsky 2025, 2026
# SPDX-License-Identifier: MPL-2.0

"""Exercise the independent stacks example on a disposable SSH-accessible VM.

This test reboots the VM. See tests/README.md for prerequisites and usage.
Only the Python standard library is required on the controller.
"""

import argparse
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", required=True)
    parser.add_argument("--port", type=int, default=22)
    parser.add_argument("--user", required=True)
    parser.add_argument("--identity", type=Path, required=True)
    parser.add_argument("--known-hosts", type=Path, required=True)
    parser.add_argument("--provider-dir", type=Path, required=True)
    parser.add_argument("--workdir", type=Path, required=True)
    parser.add_argument("--cli", default="tofu")
    parser.add_argument("--allow-reboot", action="store_true", required=True)
    args = parser.parse_args()
    work = args.workdir.resolve()
    work.mkdir(parents=True, exist_ok=False)
    fixture = work / "configuration"
    shutil.copytree(Path(__file__).resolve().parents[1] / "examples/independent-stacks", fixture)
    logs = work / "logs"
    logs.mkdir()
    ssh = ["ssh", "-F", "/dev/null", "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes",
           "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=5",
           "-o", f"UserKnownHostsFile={args.known_hosts.resolve()}",
           "-i", str(args.identity.resolve()), "-p", str(args.port), f"{args.user}@{args.host}"]
    env = dict(os.environ, TF_IN_AUTOMATION="1", CHECKPOINT_DISABLE="1")
    cli_config = work / "terraform.rc"
    cli_config.write_text("provider_installation {\n  dev_overrides {\n"
                          f'    "savely-krasovsky/homelab-helpers" = {json.dumps(str(args.provider_dir.resolve()))}\n'
                          "  }\n  direct {}\n}\n")
    env["TF_CLI_CONFIG_FILE"] = str(cli_config)
    report = {"checks": []}
    sequence = 0

    def run(command, expected=0, timeout=180):
        nonlocal sequence
        sequence += 1
        result = subprocess.run(command, cwd=fixture, env=env, text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
        log = logs / f"{sequence:03d}.log"
        log.write_text(result.stdout)
        if expected is not None and result.returncode != expected:
            raise RuntimeError(f"Exit {result.returncode}, expected {expected}: {shlex.join(command)}\n"
                               f"{result.stdout[-4000:]}\nFull output: {log}")
        return result

    def remote(*command, **kwargs):
        return run(ssh + [shlex.join(command)], **kwargs)

    def tofu(action, *options, **kwargs):
        return run([args.cli, action, "-no-color", *options], **kwargs)

    def check(condition, message):
        if not condition:
            raise RuntimeError(message)

    def passed(message):
        report["checks"].append(message)
        (work / "result.json").write_text(json.dumps(report, indent=2) + "\n")
        print("PASS:", message, flush=True)

    def invocations(names=("alpha", "beta")):
        return {name: remote("systemctl", "--user", "show", "--value", "-p", "InvocationID",
                             f"example-{name}.service").stdout.strip() for name in names}

    def active(*names):
        remote("systemctl", "--user", "is-active", *[f"example-{name}.service" for name in names])

    def body(name, expected):
        actual = remote("podman", "exec", f"example-{name}", "wget", "-q", "-O-",
                        "http://127.0.0.1:8080/").stdout.strip()
        check(actual == expected, f"{name} returned {actual!r}, expected {expected!r}")

    # Require an unused fixture namespace; never adopt or replace existing secrets.
    config_dir = remote("systemd-path", "user-configuration").stdout.strip()
    state_dir = remote("systemd-path", "user-state-private").stdout.strip()
    uid = remote("id", "-u").stdout.strip()
    for name in ["alpha", "beta", "network"]:
        remote("test", "!", "-e", f"{state_dir}/terraform-quadlet/deployments/example-{name}")
    for name in ["alpha", "beta"]:
        for path in [f"{config_dir}/containers/systemd/example-{name}.container",
                     f"{config_dir}/systemd/user/example-{name}.target"]:
            remote("test", "!", "-e", path)
        exists = remote("podman", "secret", "exists", f"example-{name}", expected=None)
        check(exists.returncode == 1, f"Secret namespace is not empty or Podman failed: {name}")
    remote("systemctl", "--user", "enable", "--now", "podman.socket")
    report["podman"] = remote("podman", "version").stdout.strip()
    report["systemd"] = remote("systemctl", "--version").stdout.splitlines()[0]
    report["cli"] = run([args.cli, "version", "-json"]).stdout.strip()

    override = {"provider": {"quadlet": {
        "transport": "ssh", "host": args.host, "port": args.port, "user": args.user,
        "private_key_file": str(args.identity.resolve()), "known_hosts_file": str(args.known_hosts.resolve())}},
        "resource": {"quadlet_deployment": {"app": {
            "triggers": {"password": "${quadlet_podman_secret.test[each.key].revision}"}}}}}
    (fixture / "smoke_override.tf.json").write_text(json.dumps(override, indent=2))
    secrets = {"resource": {"quadlet_podman_secret": {"test": {
        "for_each": "${var.messages}", "name": "example-${each.key}",
        "value_wo": "synthetic-smoke-value", "version": "1"}}},
        "output": {"secret_revisions": {"value": "${{for name, secret in quadlet_podman_secret.test : name => secret.revision}}"}}}
    (fixture / "smoke.tf.json").write_text(json.dumps(secrets, indent=2))
    container = fixture / "app.container.tftpl"
    container.write_text(container.read_text().replace(
        "[Container]\n", "[Container]\nSecret=example-${name},type=env,target=SMOKE_SECRET\n"))

    def messages(alpha="Hello from alpha", beta="Hello from beta"):
        values = {"beta": beta}
        if alpha is not None:
            values["alpha"] = alpha
        (fixture / "smoke.auto.tfvars.json").write_text(json.dumps({"messages": values}))

    messages()
    print("Applying to the disposable VM; the first image pull may take several minutes.", flush=True)
    tofu("apply", "-auto-approve", timeout=1000)
    active("alpha", "beta", "network")
    body("alpha", "Hello from alpha")
    body("beta", "Hello from beta")
    tofu("plan", "-detailed-exitcode")
    initial = invocations()
    check(all(initial.values()), "Missing initial service invocations")
    passed("initial apply starts both applications; the next plan is empty")

    messages(alpha="Updated alpha")
    tofu("apply", "-auto-approve")
    updated = invocations()
    check(updated["alpha"] != initial["alpha"], "Alpha did not restart")
    check(updated["beta"] == initial["beta"], "Updating alpha restarted beta")
    body("alpha", "Updated alpha")
    tofu("plan", "-detailed-exitcode")
    passed("changing alpha restarts only alpha; the next plan is empty")

    revisions = json.loads(tofu("output", "-json", "secret_revisions").stdout)
    remote("podman", "secret", "rm", "example-alpha")
    tofu("apply", "-auto-approve")
    recreated = json.loads(tofu("output", "-json", "secret_revisions").stdout)
    after_secret = invocations()
    check(recreated["alpha"] != revisions["alpha"], "Recreated secret retained its revision")
    check(recreated["beta"] == revisions["beta"], "Beta's secret revision changed during refresh")
    check(after_secret["alpha"] != updated["alpha"], "Secret recreation did not restart alpha")
    check(after_secret["beta"] == updated["beta"], "Secret recreation restarted beta")
    tofu("plan", "-detailed-exitcode")
    passed("recreating a secret at version 1 changes its revision and restarts only its consumer")

    runtime_dir = f"/run/user/{uid}/systemd/user/example-alpha.service.d"
    fault = runtime_dir + "/99-smoke-failure.conf"
    remote("test", "!", "-e", fault)
    remote("mkdir", "-p", runtime_dir)
    remote("sh", "-c", 'printf "%s\\n" "[Service]" "Restart=no" "ExecStartPre=/bin/false" > "$1"', "smoke", fault)
    remote("systemctl", "--user", "daemon-reload")
    messages(alpha="Recovered alpha")
    failed = tofu("apply", "-auto-approve", expected=None)
    check(failed.returncode != 0 and "Deployment failed" in failed.stdout, "Expected an activation failure")
    record_path = f"{state_dir}/terraform-quadlet/deployments/example-alpha/deployment.json"
    record = json.loads(remote("cat", record_path).stdout)
    check(record["applied_revision"] == "", "Failed activation committed a revision")
    remote("rm", "--", fault)
    remote("systemctl", "--user", "daemon-reload")
    remote("systemctl", "--user", "reset-failed", "example-alpha.service", "example-alpha.target")
    tofu("apply", "-auto-approve")
    active("alpha", "beta", "network")
    body("alpha", "Recovered alpha")
    check(invocations()["beta"] == after_secret["beta"], "Recovery restarted beta")
    record = json.loads(remote("cat", record_path).stdout)
    check(bool(record["applied_revision"]), "Recovery did not commit a revision")
    tofu("plan", "-detailed-exitcode")
    passed("a real systemd activation failure leaves the record unfinished; retry with identical files recovers alpha")

    old_boot = remote("cat", "/proc/sys/kernel/random/boot_id").stdout.strip()
    before_reboot = invocations()
    remote("sudo", "-n", "systemctl", "reboot", expected=None)
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        time.sleep(2)
        boot = remote("cat", "/proc/sys/kernel/random/boot_id", expected=None, timeout=10)
        if boot.returncode == 0 and boot.stdout.strip() != old_boot:
            ready = remote("systemctl", "--user", "is-active", "example-alpha.service",
                           "example-beta.service", "example-network.service", expected=None)
            if ready.returncode == 0:
                break
    else:
        raise RuntimeError("VM did not reboot and start both applications within two minutes")
    after_reboot = invocations()
    check(all(after_reboot[name] != before_reboot[name] for name in after_reboot), "Services did not restart after reboot")
    body("alpha", "Recovered alpha")
    body("beta", "Hello from beta")
    tofu("plan", "-detailed-exitcode")
    passed("after a real VM reboot both applications and their shared network start; the plan stays empty")

    messages(alpha=None)
    tofu("apply", "-auto-approve")
    active("beta", "network")
    alpha = remote("systemctl", "--user", "is-active", "example-alpha.service", expected=None)
    check(alpha.returncode != 0, "Removed alpha remains active")
    check(invocations(["beta"])["beta"] == after_reboot["beta"], "Deleting alpha restarted beta")
    tofu("plan", "-detailed-exitcode")
    passed("deleting alpha preserves beta and the shared network without restarting beta")

    tofu("destroy", "-auto-approve")
    state = json.loads(tofu("show", "-json").stdout)
    check(not state.get("values", {}).get("root_module", {}).get("resources"), "Destroy left managed resources in state")
    for name in ["alpha", "beta", "network"]:
        result = remote("systemctl", "--user", "is-active", f"example-{name}.service", expected=None)
        check(result.returncode != 0, f"Destroy left {name} active")
    passed("destroy removes all managed resources and stops all example services")
    print(f"Smoke test complete. Results: {work / 'result.json'}", flush=True)


if __name__ == "__main__":
    main()
