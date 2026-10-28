package remote

import "testing"

func TestParseListen(t *testing.T) {
	cases := []struct {
		name, listen string
		port         int
		bind         string
		outPort      int
		ip           string
		bad          bool
	}{
		{"default is every interface on -port", "", 7000, ":7000", 7000, "", false},
		{"ip alone uses -port", "192.168.1.5", 7001, "192.168.1.5:7001", 7001, "192.168.1.5", false},
		{"ip and port", "192.168.1.5:7100", 7000, "192.168.1.5:7100", 7100, "192.168.1.5", false},
		{"ipv6 with port", "[fe80::1]:7100", 7000, "[fe80::1]:7100", 7100, "fe80::1", false},
		{"bare ipv6", "fd7a::1", 7000, "[fd7a::1]:7000", 7000, "fd7a::1", false},
		{"port only", ":7200", 7000, ":7200", 7200, "", false},
		{"loopback", "127.0.0.1:7000", 7000, "127.0.0.1:7000", 7000, "127.0.0.1", false},
		{"a name is refused", "my-pc.local:7000", 7000, "", 0, "", true},
		{"port zero", "10.0.0.1:0", 7000, "", 0, "", true},
		{"port too large", "10.0.0.1:70000", 7000, "", 0, "", true},
		{"port is not a number", "10.0.0.1:abc", 7000, "", 0, "", true},
		{"bad -port without -listen", "", 0, "", 0, "", true},
	}
	for _, c := range cases {
		bind, port, ip, err := ParseListen(c.listen, c.port)
		if (err != nil) != c.bad {
			t.Errorf("%s: err = %v, want error=%v", c.name, err, c.bad)
			continue
		}
		if c.bad {
			continue
		}
		if bind != c.bind || port != c.outPort || ip != c.ip {
			t.Errorf("%s: ParseListen(%q, %d) = %q, %d, %q; want %q, %d, %q", c.name, c.listen, c.port, bind, port, ip, c.bind, c.outPort, c.ip)
		}
	}
}
