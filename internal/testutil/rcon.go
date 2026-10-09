package testutil

import (
	"testing"

	"github.com/gorcon/rcon"
	"github.com/gorcon/rcon/rcontest"
)

// RCONServer starts a fake RCON server that answers every command with handler.
func RCONServer(t *testing.T, password string, handler func(cmd string) string) string {
	t.Helper()
	srv := rcontest.NewServer(
		rcontest.SetSettings(rcontest.Settings{Password: password}),
		rcontest.SetCommandHandler(func(c *rcontest.Context) {
			_, _ = rcon.NewPacket(rcon.SERVERDATA_RESPONSE_VALUE, c.Request().ID, handler(c.Request().Body())).WriteTo(c.Conn())
		}),
	)
	t.Cleanup(srv.Close)
	return srv.Addr()
}
