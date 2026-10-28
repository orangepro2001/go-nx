package remote

import "testing"

func TestRatesNormalized(t *testing.T) {
	for _, c := range []struct {
		name     string
		in, want Rates
	}{
		{"the defaults stay as they are", DefaultRates(), Rates{10, 30, 2}},
		{"nothing set means the default base only", Rates{}, Rates{Base: DefaultFPS}},
		{"a negative base is the default", Rates{Base: -5, Active: 30, Idle: 2}, Rates{10, 30, 2}},
		{"a base beyond the fastest is lowered", Rates{Base: 500, Active: 600, Idle: 2}, Rates{Base: MaxFPS, Idle: 2}},
		{"a boost that is not faster is no boost", Rates{Base: 20, Active: 20, Idle: 2}, Rates{Base: 20, Idle: 2}},
		{"a boost slower than the base is no boost", Rates{Base: 20, Active: 5}, Rates{Base: 20}},
		{"a boost is held to the fastest a viewer can ask for", Rates{Base: 10, Active: 100}, Rates{Base: 10, Active: MaxActiveFPS}},
		{"an idle rate that is not slower is no idle rate", Rates{Base: 10, Idle: 10}, Rates{Base: 10}},
		{"an idle rate that is faster is no idle rate", Rates{Base: 10, Idle: 25}, Rates{Base: 10}},
		{"a negative idle rate is no idle rate", Rates{Base: 10, Idle: -1}, Rates{Base: 10}},
		{"the slowest base cannot slow down", Rates{Base: 1, Active: 30, Idle: 1}, Rates{Base: 1, Active: 30}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.in.Normalized(); got != c.want {
				t.Errorf("%#v.Normalized() = %#v, want %#v", c.in, got, c.want)
			}
			if again := c.in.Normalized().Normalized(); again != c.want {
				t.Errorf("normalizing twice gave %#v, want %#v", again, c.want)
			}
		})
	}
}

func TestRatesBoostFollowsThePolicy(t *testing.T) {
	b := DefaultRates().Boost()
	if got := b.Rate(); got != 10 {
		t.Fatalf("rate before any input = %d, want 10", got)
	}
	b.Touch()
	if got := b.Rate(); got != 30 {
		t.Fatalf("rate after input = %d, want 30", got)
	}
	off := Rates{Base: 10, Active: 0, Idle: 2}.Boost()
	off.Touch()
	if got := off.Rate(); got != 10 {
		t.Fatalf("a policy without a boost sped up to %d", got)
	}
}

func TestRatesString(t *testing.T) {
	if got, want := DefaultRates().String(), "watching 10/s, working 30/s, idle 2/s"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got, want := (Rates{Base: 15}).String(), "watching 15/s, working as watching, idle as watching"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
