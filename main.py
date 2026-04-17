#!/usr/bin/env python3
"""QuickTunnel launcher for easier local execution."""

from __future__ import annotations

import argparse
import os
import shutil
import socket
import subprocess
import sys
import time
from pathlib import Path
from typing import Dict, Iterable, List, Optional
from urllib.error import HTTPError, URLError
from urllib.request import urlopen

ROOT = Path(__file__).resolve().parent
SERVER_DIR = ROOT / "server"
RELAY_DIR = ROOT / "relay"
CLIENT_DIR = ROOT / "client"

WINDOWS_TOOL_FALLBACKS = {
    "go": [
        Path("C:/Program Files/Go/bin/go.exe"),
    ],
    "docker": [
        Path("C:/Program Files/Docker/Docker/resources/bin/docker.exe"),
    ],
}

DEFAULT_ENV = {
    "DB_URL": "postgres://quicktunnel:quicktunnel@localhost:5432/quicktunnel?sslmode=disable",
    "REDIS_URL": "redis://localhost:6379/0",
    "JWT_SECRET": "change-me-in-production",
    "SERVER_PORT": "8080",
    "STUN_SERVER": "stun.l.google.com:19302",
    "LOG_LEVEL": "info",
    "RELAY_PORT": "3478",
    "HEALTH_PORT": "8081",
    "COORD_SERVER_URL": "http://localhost:8080",
    "RELAY_NAME": "relay-local",
}


class CommandError(RuntimeError):
    """Raised when a launcher command fails."""


def merged_env(overrides: Optional[Dict[str, str]] = None) -> Dict[str, str]:
    """Return process environment with QuickTunnel defaults applied."""
    env = os.environ.copy()
    for key, value in DEFAULT_ENV.items():
        env.setdefault(key, value)

    work_file = ROOT / "go.work"
    if work_file.exists():
        env.setdefault("GOWORK", str(work_file))

    if os.name == "nt":
        path_entries = env.get("PATH", "").split(os.pathsep)
        windows_bins = [
            "C:/Program Files/Go/bin",
            "C:/Program Files/Docker/Docker/resources/bin",
        ]
        for entry in windows_bins:
            if Path(entry).exists() and entry not in path_entries:
                path_entries.append(entry)
        env["PATH"] = os.pathsep.join(path_entries)

    if overrides:
        for key, value in overrides.items():
            env[key] = str(value)

    return env


def resolve_tool(tool: str) -> Optional[str]:
    """Resolve a binary from PATH, then from known Windows install locations."""
    resolved = shutil.which(tool)
    if resolved:
        return resolved

    if os.name != "nt":
        return None

    for candidate in WINDOWS_TOOL_FALLBACKS.get(tool, []):
        if candidate.exists():
            return str(candidate)

    return None


def require_tools(tools: Iterable[str]) -> Dict[str, str]:
    """Ensure required binaries are available and return resolved executable paths."""
    resolved: Dict[str, str] = {}
    missing: List[str] = []

    for tool in tools:
        path = resolve_tool(tool)
        if path is None:
            missing.append(tool)
        else:
            resolved[tool] = path

    if not missing:
        return resolved

    hints: List[str] = []
    if "go" in missing:
        hints.append("winget install -e --id GoLang.Go")
    if "docker" in missing:
        hints.append("winget install -e --id Docker.DockerDesktop")

    hint_text = ""
    if hints:
        hint_text = " Install with: " + " ; ".join(hints)
    hint_text += " If you just installed tools, open a new terminal and retry."

    raise CommandError("Missing required tools: " + ", ".join(missing) + "." + hint_text)


def run(command: List[str], cwd: Path, env: Dict[str, str]) -> None:
    """Run a command and raise on failure."""
    print("[run] " + " ".join(command) + " (cwd=" + str(cwd) + ")")
    result = subprocess.run(
        command,
        cwd=str(cwd),
        env=env,
        text=True,
        encoding="utf-8",
        errors="replace",
        check=False,
    )
    if result.returncode != 0:
        raise CommandError("Command failed (" + str(result.returncode) + "): " + " ".join(command))


def ensure_docker_daemon(docker_bin: str, env: Dict[str, str]) -> None:
    """Ensure Docker daemon is running before compose operations."""
    result = subprocess.run(
        [docker_bin, "info"],
        cwd=str(ROOT),
        env=env,
        text=True,
        encoding="utf-8",
        errors="replace",
        capture_output=True,
        check=False,
    )
    if result.returncode == 0:
        return

    raise CommandError(
        "Docker is installed but not running. Start Docker Desktop and retry."
    )


def start_background(name: str, command: List[str], cwd: Path, env: Dict[str, str]) -> subprocess.Popen:
    """Start a background process and return its handle."""
    print("[" + name + "] " + " ".join(command) + " (cwd=" + str(cwd) + ")")
    proc = subprocess.Popen(
        command,
        cwd=str(cwd),
        env=env,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    print("[" + name + "] pid=" + str(proc.pid))
    return proc


def stop_background(name: str, proc: Optional[subprocess.Popen]) -> None:
    """Stop a background process gracefully."""
    if proc is None or proc.poll() is not None:
        return

    print("[" + name + "] stopping pid=" + str(proc.pid))
    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait(timeout=5)


def wait_http_ok(url: str, timeout_seconds: int = 60) -> None:
    """Wait until an HTTP endpoint responds with 2xx status."""
    deadline = time.time() + timeout_seconds
    while time.time() < deadline:
        try:
            with urlopen(url, timeout=2) as response:
                if 200 <= response.status < 300:
                    print("[health] ok: " + url)
                    return
        except (URLError, HTTPError, OSError):
            pass
        time.sleep(1)

    raise CommandError("Health check timeout: " + url)


def validate_auth_args(args: argparse.Namespace) -> None:
    """Validate join authentication flags."""
    if args.api_key:
        return

    if not args.email or not args.password:
        raise CommandError("Use --api-key OR --email plus --password.")


def configure_client(args: argparse.Namespace, env: Dict[str, str], go_bin: str) -> None:
    """Apply quicktunnel client config and authentication."""
    validate_auth_args(args)

    config_cmd = [go_bin, "run", "./cmd/quicktunnel", "config"]
    settings = [
        ("server_url", args.server_url),
        ("network_id", args.network_id),
        ("device_name", args.device_name),
        ("log_level", args.log_level),
        ("wg_listen_port", str(args.wg_listen_port)),
        ("stun_server", args.stun_server),
    ]
    for key, value in settings:
        config_cmd.extend(["--set", key + "=" + value])

    run(config_cmd, CLIENT_DIR, env)

    if args.api_key:
        run(
            [go_bin, "run", "./cmd/quicktunnel", "config", "--set", "api_key=" + args.api_key],
            CLIENT_DIR,
            env,
        )
    else:
        run(
            [
                go_bin,
                "run",
                "./cmd/quicktunnel",
                "login",
                "--email",
                args.email,
                "--password",
                args.password,
            ],
            CLIENT_DIR,
            env,
        )


def run_join(args: argparse.Namespace, env: Dict[str, str], go_bin: str) -> None:
    """Configure and start quicktunnel client in foreground."""
    configure_client(args, env, go_bin)
    run([go_bin, "run", "./cmd/quicktunnel", "up"], CLIENT_DIR, env)


def command_server(args: argparse.Namespace) -> None:
    """Run server command."""
    needed = ["go"]
    if not args.no_deps:
        needed.append("docker")
    bins = require_tools(needed)
    go_bin = bins["go"]

    env = merged_env({"SERVER_PORT": str(args.server_port)})

    if not args.no_deps:
        docker_bin = bins["docker"]
        ensure_docker_daemon(docker_bin, env)
        run([docker_bin, "compose", "up", "-d", "postgres", "redis"], ROOT, env)

    run([go_bin, "run", "./cmd/server"], SERVER_DIR, env)


def command_relay(args: argparse.Namespace) -> None:
    """Run relay command."""
    bins = require_tools(["go"])
    go_bin = bins["go"]

    env = merged_env(
        {
            "RELAY_PORT": str(args.relay_port),
            "HEALTH_PORT": str(args.health_port),
            "COORD_SERVER_URL": args.coord_server_url,
        }
    )
    run([go_bin, "run", "./cmd/relay"], RELAY_DIR, env)


def command_join(args: argparse.Namespace) -> None:
    """Run join command."""
    bins = require_tools(["go"])
    go_bin = bins["go"]
    env = merged_env()
    run_join(args, env, go_bin)


def command_all(args: argparse.Namespace) -> None:
    """Run server + relay in background and join in foreground."""
    needed = ["go"]
    if not args.no_deps:
        needed.append("docker")
    bins = require_tools(needed)
    go_bin = bins["go"]

    server_url = args.server_url if args.server_url else "http://localhost:" + str(args.server_port)
    coord_url = args.coord_server_url if args.coord_server_url else server_url

    env = merged_env(
        {
            "SERVER_PORT": str(args.server_port),
            "RELAY_PORT": str(args.relay_port),
            "HEALTH_PORT": str(args.health_port),
            "COORD_SERVER_URL": coord_url,
        }
    )

    if not args.no_deps:
        docker_bin = bins["docker"]
        ensure_docker_daemon(docker_bin, env)
        run([docker_bin, "compose", "up", "-d", "postgres", "redis"], ROOT, env)

    server_proc: Optional[subprocess.Popen] = None
    relay_proc: Optional[subprocess.Popen] = None

    try:
        server_proc = start_background("server", [go_bin, "run", "./cmd/server"], SERVER_DIR, env)
        wait_http_ok(server_url.rstrip("/") + "/health")

        relay_proc = start_background("relay", [go_bin, "run", "./cmd/relay"], RELAY_DIR, env)
        wait_http_ok("http://localhost:" + str(args.health_port) + "/health")

        args.server_url = server_url
        run_join(args, env, go_bin)
    finally:
        stop_background("relay", relay_proc)
        stop_background("server", server_proc)


def add_join_arguments(parser: argparse.ArgumentParser, server_url_default: Optional[str]) -> None:
    """Attach common join args to parser."""
    parser.add_argument("--server-url", default=server_url_default, help="Server URL")
    parser.add_argument("--network-id", required=True, help="Network ID")
    parser.add_argument("--device-name", default=socket.gethostname(), help="Device name")
    parser.add_argument("--log-level", default="info", help="Client log level")
    parser.add_argument("--wg-listen-port", type=int, default=51820, help="WireGuard listen port")
    parser.add_argument("--stun-server", default="stun.l.google.com:19302", help="STUN server")

    auth = parser.add_mutually_exclusive_group(required=True)
    auth.add_argument("--api-key", help="API key auth")
    auth.add_argument("--email", help="Email login")
    parser.add_argument("--password", help="Password for email login")


def build_parser() -> argparse.ArgumentParser:
    """Build and return CLI parser."""
    parser = argparse.ArgumentParser(description="QuickTunnel launcher")
    subparsers = parser.add_subparsers(dest="command")
    subparsers.required = True

    server_parser = subparsers.add_parser("server", help="Start server")
    server_parser.add_argument("--no-deps", action="store_true", help="Skip docker deps")
    server_parser.add_argument("--server-port", type=int, default=8080, help="Server port")
    server_parser.set_defaults(func=command_server)

    relay_parser = subparsers.add_parser("relay", help="Start relay")
    relay_parser.add_argument("--relay-port", type=int, default=3478, help="Relay port")
    relay_parser.add_argument("--health-port", type=int, default=8081, help="Relay health port")
    relay_parser.add_argument(
        "--coord-server-url",
        default="http://localhost:8080",
        help="Coordination server URL",
    )
    relay_parser.set_defaults(func=command_relay)

    join_parser = subparsers.add_parser("join", help="Configure and start client")
    add_join_arguments(join_parser, "http://localhost:8080")
    join_parser.set_defaults(func=command_join)

    all_parser = subparsers.add_parser("all", help="Start server, relay, and join")
    all_parser.add_argument("--no-deps", action="store_true", help="Skip docker deps")
    all_parser.add_argument("--server-port", type=int, default=8080, help="Server port")
    all_parser.add_argument("--relay-port", type=int, default=3478, help="Relay port")
    all_parser.add_argument("--health-port", type=int, default=8081, help="Relay health port")
    all_parser.add_argument("--coord-server-url", default=None, help="Coordination server URL")
    add_join_arguments(all_parser, None)
    all_parser.set_defaults(func=command_all)

    return parser


def main() -> int:
    """Entrypoint."""
    parser = build_parser()
    args = parser.parse_args()

    try:
        args.func(args)
        return 0
    except KeyboardInterrupt:
        print("Interrupted.")
        return 130
    except CommandError as exc:
        print("Error: " + str(exc), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
