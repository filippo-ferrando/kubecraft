"""Fake playit API for hack/e2e.sh: one tunnel, kubecraft-e2e on local port 30005."""
import http.server
import json

TUNNEL = {
    "id": "t1", "name": "kubecraft-e2e", "user_enabled": True, "offline_reasons": None,
    "tunnel_type": "minecraft-java",
    "origin": {"type": "agent", "details": {"config_data": {"fields": [
        {"name": "local_ip", "value": "127.0.0.1"}, {"name": "local_port", "value": "30005"}]}}},
    "connect_addresses": [{"type": "auto", "value": {"address": "e2e-tunnel.tun.ply.gg"}}],
}


class Handler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = json.dumps({"status": "success", "data": {"tunnels": [TUNNEL]}}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(body)


http.server.HTTPServer(("", 8080), Handler).serve_forever()
