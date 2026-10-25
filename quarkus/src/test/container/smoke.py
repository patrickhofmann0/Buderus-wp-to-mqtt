#!/usr/bin/env python3
"""Exercise the packaged service against local HTTP and MQTT/TLS test peers."""

import argparse
import http.server
import json
import os
from pathlib import Path
import shutil
import socket
import ssl
import struct
import subprocess
import threading
import time
import uuid


def wait_for(condition, timeout=20):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if condition():
            return
        time.sleep(0.05)
    raise AssertionError("service did not reach expected state")


def read_exact(peer, length):
    result = b""
    while len(result) < length:
        part = peer.recv(length - len(result))
        if not part:
            raise EOFError()
        result += part
    return result


def field(data, offset):
    length = struct.unpack_from("!H", data, offset)[0]
    offset += 2
    return data[offset:offset + length], offset + length


class Broker:
    def __init__(self, context=None):
        self.listener = socket.socket()
        self.listener.bind(("127.0.0.1", 0))
        self.listener.listen()
        self.port = self.listener.getsockname()[1]
        self.context = context
        self.messages = []
        self.will = None
        self.threads = []
        self.peers = []
        self.closed = False
        self.acceptor = threading.Thread(target=self.accept, daemon=True)
        self.acceptor.start()

    def accept(self):
        while not self.closed:
            try:
                peer, _ = self.listener.accept()
                if self.context:
                    peer = self.context.wrap_socket(peer, server_side=True)
                self.peers.append(peer)
                worker = threading.Thread(target=self.serve, args=(peer,), daemon=True)
                self.threads.append(worker)
                worker.start()
            except (OSError, ssl.SSLError):
                if self.closed:
                    return

    def serve(self, peer):
        try:
            with peer:
                while True:
                    header = read_exact(peer, 1)[0]
                    length = 0
                    multiplier = 1
                    while True:
                        digit = read_exact(peer, 1)[0]
                        length += (digit & 127) * multiplier
                        multiplier *= 128
                        if not digit & 128:
                            break
                    packet = read_exact(peer, length)
                    kind = header >> 4
                    if kind == 1:
                        _, offset = field(packet, 0)
                        flags = packet[offset + 1]
                        _, offset = field(packet, offset + 4)
                        if flags & 4:
                            topic, offset = field(packet, offset)
                            payload, offset = field(packet, offset)
                            self.will = (topic.decode(), payload.decode(), (flags >> 3) & 3)
                        peer.sendall(b"\x20\x02\x00\x00")
                    elif kind == 3:
                        topic, offset = field(packet, 0)
                        qos = (header >> 1) & 3
                        message_id = packet[offset:offset + 2] if qos else b""
                        offset += 2 if qos else 0
                        self.messages.append((topic.decode(), packet[offset:].decode(), qos, bool(header & 1)))
                        if qos:
                            peer.sendall((b"\x40" if qos == 1 else b"\x50") + b"\x02" + message_id)
                    elif kind == 6:
                        peer.sendall(b"\x70\x02" + packet[:2])
                    elif kind == 12:
                        peer.sendall(b"\xd0\x00")
                    elif kind == 14:
                        return
                    else:
                        raise AssertionError(f"unexpected MQTT packet {kind}")
        except (EOFError, OSError):
            return

    def close(self):
        self.closed = True
        self.listener.close()
        for peer in self.peers:
            try:
                peer.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            peer.close()
        for worker in self.threads:
            worker.join(2)


class Handler(http.server.BaseHTTPRequestHandler):
    paths = []
    authorization = []

    def do_GET(self):
        type(self).paths.append(self.path)
        type(self).authorization.append(self.headers.get("Authorization"))
        body = b'{"z":null,"number":900719925474099312345,"nested":{"temperature":21.2500},"raw":"ok"}'
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_):
        pass


def certificates(directory, wrong_host=False):
    subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                    "-keyout", str(directory / "key.pem"), "-out", str(directory / "cert.pem"),
                    "-days", "1", "-subj", "/CN=localhost",
                    "-addext", "subjectAltName=DNS:untrusted.invalid" if wrong_host
                    else "subjectAltName=DNS:localhost,IP:127.0.0.1"],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    subprocess.run(["keytool", "-importcert", "-noprompt", "-alias", "local",
                    "-file", str(directory / "cert.pem"), "-keystore", str(directory / "trust.p12"),
                    "-storetype", "PKCS12", "-storepass", "changeit"],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(directory / "cert.pem", directory / "key.pem")
    return context


def run(image, jar, tls, insecure, reject_mqtt, memory, cpus):
    root = Path(__file__).resolve().parents[3]
    directory = root / "target" / ("smoke-" + uuid.uuid4().hex)
    directory.mkdir(parents=True)
    broker = None
    api = None
    process = None
    name = "buderus-quarkus-smoke-" + uuid.uuid4().hex[:10]
    logs = directory / "service.log"
    try:
        context = certificates(directory, wrong_host=insecure or reject_mqtt) if tls or insecure else None
        broker = Broker(context if tls else None)
        api = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        if context:
            api.socket = context.wrap_socket(api.socket, server_side=True)
        threading.Thread(target=api.serve_forever, daemon=True).start()
        scheme = "https" if context else "http"
        base = f"{scheme}://localhost:{api.server_port}"
        env = {"API_URLS": f"heating={base}/heating,water={base}/water",
               "MQTT_BROKER": f"{'ssl' if tls else 'tcp'}://localhost:{broker.port}",
               "MQTT_CLIENT_ID": name, "MQTT_TOPIC": "comparison", "MQTT_QOS": "2",
               "MQTT_RETAIN": "true", "MQTT_PUBLISH_TIMEOUT": "5s",
               "SCHEDULE": "@every 1s", "API_TOKEN": "placeholder", "LOG_LEVEL": "debug",
               "TZ": "Europe/Berlin"}
        properties = []
        if insecure:
            env["API_INSECURE_SKIP_VERIFY"] = "true"
        if tls:
            location = "/certs/trust.p12" if image else str(directory / "trust.p12")
            properties = [f"-Djavax.net.ssl.trustStore={location}", "-Djavax.net.ssl.trustStorePassword=changeit",
                          "-Djavax.net.ssl.trustStoreType=PKCS12"]
        if image:
            command = ["docker", "run", "--rm", "--name", name, "--network", "host", "--read-only",
                       "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
                       "-v", f"{directory}:/certs:ro"]
            if memory:
                command += ["--memory", memory]
            if cpus:
                command += ["--cpus", cpus]
            for key, value in env.items():
                command += ["-e", key + "=" + value]
            command += [image] + properties
            child_env = None
        else:
            command = ["java"] + properties + ["-jar", str(Path(jar).resolve())]
            child_env = os.environ.copy()
            child_env.update(env)
        with logs.open("w") as output:
            process = subprocess.Popen(command, stdout=output, stderr=subprocess.STDOUT, env=child_env)
            if reject_mqtt:
                process.wait(timeout=15)
                assert process.returncode != 0, "MQTT TLS accepted a certificate for the wrong hostname"
                assert "service startup failed" in logs.read_text()
                assert "placeholder" not in logs.read_text()
                assert not broker.messages
                print(f"PASS {'native container' if image else 'JVM'} rejects MQTT TLS wrong hostname")
                return
            wait_for(lambda: any(m[0] == "comparison/water/z" for m in broker.messages))
            assert process.poll() is None, logs.read_text()
            assert broker.will == ("comparison/status", "offline", 1)
            assert ("comparison/status", "online", 1, True) in broker.messages
            assert ("comparison/heating/number", "900719925474099312345", 2, True) in broker.messages
            assert ("comparison/heating/nested/temperature", "21.2500", 2, True) in broker.messages
            assert ("comparison/water/raw", "ok", 2, True) in broker.messages
            assert all(value == "Bearer " + "placeholder" for value in Handler.authorization)
            # Scheduling continues after the startup poll.
            count = len(Handler.paths)
            wait_for(lambda: len(Handler.paths) > count)
            if image:
                subprocess.run(["docker", "stop", "-t", "15", name], check=True, stdout=subprocess.DEVNULL)
            else:
                process.terminate()
            process.wait(timeout=20)
            wait_for(lambda: ("comparison/status", "offline", 1, True) in broker.messages, timeout=3)
            for line in logs.read_text().splitlines():
                record = json.loads(line)
                assert "placeholder" not in line
                assert record["level"] in {"debug", "info", "warn", "error"}
            transport = "HTTPS + MQTT TLS" if tls else "insecure HTTPS + MQTT" if insecure else "HTTP + MQTT"
            print(f"PASS {'native container' if image else 'JVM'} {transport}")
    except BaseException:
        if logs.exists():
            print(logs.read_text())
        raise
    finally:
        if image:
            subprocess.run(["docker", "rm", "-f", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if process and process.poll() is None:
            process.kill()
            process.wait(timeout=5)
        if api:
            api.shutdown()
            api.server_close()
        if broker:
            broker.close()
        shutil.rmtree(directory)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    target = parser.add_mutually_exclusive_group(required=True)
    target.add_argument("--image")
    target.add_argument("--jar")
    transport = parser.add_mutually_exclusive_group()
    transport.add_argument("--tls", action="store_true")
    transport.add_argument("--insecure", action="store_true")
    transport.add_argument("--reject-mqtt-hostname", action="store_true")
    parser.add_argument("--memory", help="optional Docker memory limit, for example 128m")
    parser.add_argument("--cpus", help="optional Docker CPU limit, for example 0.1")
    args = parser.parse_args()
    run(args.image, args.jar, args.tls or args.reject_mqtt_hostname, args.insecure,
        args.reject_mqtt_hostname, args.memory, args.cpus)
