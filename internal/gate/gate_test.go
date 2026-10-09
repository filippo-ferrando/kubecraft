package gate

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/k8s"
)

func TestHandshakeRoundTrip(t *testing.T) {
	in := Handshake{Protocol: 772, Host: "survival.mine.example.org", Port: 25565, NextState: 2}
	got, err := ReadHandshake(bufio.NewReader(bytes.NewReader(in.Encode())))
	if err != nil || got != in {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestHandshakeSuffixAndCase(t *testing.T) {
	// Forge adds "\x00FML3\x00" to the address; it must survive a rewrite.
	raw := Handshake{Protocol: 47, Host: "Fried-Dome.tun.PLY.gg.", Suffix: "\x00FML3\x00", Port: 1, NextState: 1}.Encode()
	hs, err := ReadHandshake(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if hs.Host != "fried-dome.tun.ply.gg" || hs.Suffix != "\x00FML3\x00" {
		t.Fatalf("parsed %+v", hs)
	}
	hs.Host = "survival.mine.internal"
	again, _ := ReadHandshake(bufio.NewReader(bytes.NewReader(hs.Encode())))
	if again.Host != "survival.mine.internal" || again.Suffix != "\x00FML3\x00" {
		t.Fatalf("rewrite lost data: %+v", again)
	}
}

func TestHandshakeRejects(t *testing.T) {
	cases := map[string][]byte{
		"legacy ping": {0xFE, 0x01},
		"empty":       {},
		"huge length": {0xFF, 0xFF, 0x7F},
		"wrong id":    {0x02, 0x05, 0x00},
		"bad varint":  {0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01},
		"short host":  {0x05, 0x00, 0x01, 0x40, 0x61, 0x62},
	}
	for name, b := range cases {
		if _, err := ReadHandshake(bufio.NewReader(bytes.NewReader(b))); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// upstream records the handshake it receives and answers "pong" to "ping".
func upstream(t *testing.T) (string, chan Handshake) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	got := make(chan Handshake, 8)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				hs, err := ReadHandshake(br)
				if err != nil {
					return
				}
				got <- hs
				extra := make([]byte, 4)
				if _, err := io.ReadFull(br, extra); err == nil && string(extra) == "ping" {
					_, _ = c.Write([]byte("pong"))
				}
			}()
		}
	}()
	return l.Addr().String(), got
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func dialPing(t *testing.T, port int, host string) string {
	t.Helper()
	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return "refused"
	}
	defer c.Close()
	msg := append(Handshake{Protocol: 772, Host: host, Port: 25565, NextState: 2}.Encode(), "ping"...)
	_, _ = c.Write(msg)
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	reply := make([]byte, 4)
	if _, err := io.ReadFull(c, reply); err != nil {
		return "no reply"
	}
	return string(reply)
}

func TestGateRoutesByPort(t *testing.T) {
	up, got := upstream(t)
	g := New("127.0.0.1", up, 8)
	defer g.Close()
	pa, pb := freePort(t), freePort(t)
	g.Sync(map[int]string{pa: "survival.mine.internal", pb: "creative.mine.internal"})

	// The client hostname (the playit address) is ignored: the port decides the server.
	if r := dialPing(t, pa, "fried-dome.tun.ply.gg"); r != "pong" {
		t.Fatalf("port a: %s", r)
	}
	if hs := <-got; hs.Host != "survival.mine.internal" || hs.NextState != 2 {
		t.Fatalf("upstream saw %+v", hs)
	}
	// A forged hostname on port b still only reaches creative.
	if r := dialPing(t, pb, "survival.mine.internal"); r != "pong" {
		t.Fatalf("port b: %s", r)
	}
	if hs := <-got; hs.Host != "creative.mine.internal" {
		t.Fatalf("forged name reached %s", hs.Host)
	}

	// Removing a route closes its port.
	g.Sync(map[int]string{pb: "creative.mine.internal"})
	if r := dialPing(t, pa, "x"); r != "refused" {
		t.Fatalf("closed port answered: %s", r)
	}
	if len(g.Ports()) != 1 {
		t.Fatalf("ports: %v", g.Ports())
	}
}

func TestRoutesFromServices(t *testing.T) {
	svc := func(name, port, host string, playit bool) *corev1.Service {
		return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "minecraft",
			Labels:      map[string]string{k8s.LabelPlayit: strconv.FormatBool(playit)},
			Annotations: map[string]string{k8s.AnnotationPlayitPort: port, k8s.AnnotationRouterHost: host}}}
	}
	cs := fake.NewClientset(
		svc("mc-a", "30001", "A.mine.internal", true),
		svc("mc-b", "30002", "b.mine.internal", false), // not playit
		svc("mc-c", "80", "c.mine.internal", true),     // privileged port
		svc("mc-d", "30004", "d.mine.internal", true),
		svc("mc-e", "30004", "e.mine.internal", true), // clash: neither routed
	)
	r, err := RoutesFromServices(context.Background(), cs, "minecraft")
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 1 || r[30001] != "a.mine.internal" {
		t.Fatalf("routes: %v", r)
	}
}
