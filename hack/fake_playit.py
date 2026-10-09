"""Fake playit API for hack/e2e.sh: one self-managed agent, tunnels kept in memory.
Shapes copied from real responses; kubecraft-<slug> gets the address <slug>-tunnel.tun.ply.gg."""
import http.server
import json
import uuid

AGENT = "6f1c2c6e-0000-4000-8000-00000000e2e0"
TUNNELS = {}


def tunnel(tid, name, fields):
    return {
        "id": tid, "name": name, "user_enabled": True, "offline_reasons": None,
        "tunnel_type": "minecraft-java",
        "origin": {"type": "agent", "details": {"agent_id": AGENT, "config_data": {"fields": fields}}},
        "connect_addresses": [{"type": "auto", "value": {"address": name.removeprefix("kubecraft-") + "-tunnel.tun.ply.gg"}}],
    }


class Handler(http.server.BaseHTTPRequestHandler):
    def reply(self, status, data, code=200):
        body = json.dumps({"status": status, "data": data}).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        req = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
        if self.path == "/v1/agents/rundata":
            self.reply("success", {"agent_id": AGENT})
        elif self.path == "/v1/tunnels/list":
            self.reply("success", {"tunnels": list(TUNNELS.values())})
        elif self.path == "/v1/tunnels/create":
            tid = str(uuid.uuid4())
            TUNNELS[tid] = tunnel(tid, req["name"], req["origin"]["data"]["config"]["fields"])
            self.reply("success", {"id": tid})
        elif self.path == "/tunnels/delete":
            found = TUNNELS.pop(req.get("tunnel_id"), None)
            self.reply("success" if found else "fail", None if found else "TunnelNotFound")
        else:
            self.reply("error", {"type": "not-found"}, 404)

    def log_message(self, *args):
        pass


http.server.HTTPServer(("", 8080), Handler).serve_forever()
