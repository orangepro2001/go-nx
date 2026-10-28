package app

import (
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/profiles"
	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/update"
)

func TestStartsOnTheDevicesPageWithTheAddressFocused(t *testing.T) {
	r := newRig(t)
	if !r.has("Devices") || !r.has("Quick connect") || !r.has("Saved devices") {
		t.Fatalf("the devices page is not showing: %q", r.texts())
	}
	if r.a.ui.Focused() != "quick.addr" {
		t.Errorf("focus is on %q: the address field should have it, so that typing and Enter connect", r.a.ui.Focused())
	}
	for _, s := range r.texts() {
		if strings.ContainsAny(s, "{}") {
			t.Errorf("a placeholder is showing: %q", s)
		}
	}
}

func TestQuickConnectWithoutAnAddressSaysSo(t *testing.T) {
	r := newRig(t)
	r.pressButton("quick.connect")
	if !r.has("Enter the other PC's address") {
		t.Errorf("no notice: %q", r.texts())
	}
	if len(r.a.tabs) != 0 {
		t.Error("a tab was opened without an address")
	}
}

func TestQuickConnectOpensATabAndFlowsThroughTheFirstTimeCheck(t *testing.T) {
	r := newRig(t)
	r.typ("10.0.0.7")
	r.key(kit.KeyPress{Key: kit.KeyTab})
	r.typ("pairing-code")
	r.key(kit.KeyPress{Key: kit.KeyEnter}) // Enter in the password field connects
	if len(r.a.tabs) != 1 || r.a.tabs[0].addr != "10.0.0.7:7000" || r.a.active != 1 {
		t.Fatalf("tabs %+v active %d: the default port should have been added", r.a.tabs, r.a.active)
	}
	// A host never seen before: nothing is sent to it until the person has compared IDs.
	r.wait("the host's ID to be asked about", func() bool { return r.a.tabs[0].confirm != nil })
	if r.conn.dials != 0 {
		t.Fatal("the host was dialled before its identity was confirmed")
	}
	if !r.has("First connection to 10.0.0.7:7000") || !r.has("Trust and connect") {
		t.Errorf("the question is not showing: %q", r.texts())
	}
	// The ID is shown in groups of four, a character at a time.
	shown := strings.Join(r.texts(), "")
	for _, c := range "A1B2C3D4E5F67A8B" {
		if !strings.ContainsRune(shown, c) {
			t.Fatalf("the ID is not on screen (missing %q): %q", c, r.texts())
		}
	}
	if r.a.ui.Focused() != "sess.trust" {
		t.Errorf("focus is on %q: Enter should confirm", r.a.ui.Focused())
	}
	r.key(kit.KeyPress{Key: kit.KeyEnter})
	r.wait("the tab to go live", func() bool { return r.a.tabs[0].state == tabLive })
	if fp, ok := r.conn.Pinned("10.0.0.7:7000"); !ok || fp != r.conn.probeFP {
		t.Errorf("the identity was not pinned: %q %v", fp, ok)
	}
	if len(r.conn.passwords) != 1 || r.conn.passwords[0] != "pairing-code" {
		t.Errorf("the password sent was %q: it goes only after the host is pinned", r.conn.passwords)
	}
}

func TestDecliningTheFirstTimeCheckSendsNothing(t *testing.T) {
	r := newRig(t)
	r.a.quickAddr = "10.0.0.8"
	r.a.connectQuick()
	r.wait("the question", func() bool { return r.a.tabs[0].confirm != nil })
	r.key(kit.KeyPress{Key: kit.KeyEscape})
	r.wait("the tab to fail", func() bool { return r.a.tabs[0].state == tabError })
	if r.conn.dials != 0 {
		t.Error("a host that was declined was dialled")
	}
	if _, pinned := r.conn.Pinned("10.0.0.8:7000"); pinned {
		t.Error("a declined host was pinned")
	}
	if !r.has("You did not confirm this host's identity, so nothing was sent to it.") {
		t.Errorf("no explanation: %q", r.texts())
	}
	r.key(kit.KeyPress{Key: kit.KeyEscape}) // Escape on a failed tab closes it
	if len(r.a.tabs) != 0 || r.a.active != 0 {
		t.Errorf("tabs %d active %d after Escape", len(r.a.tabs), r.a.active)
	}
}

func TestAKnownHostGoesStraightThroughAndFailuresOfferRetry(t *testing.T) {
	r := newRig(t)
	r.conn.pinned["10.0.0.9:7000"] = "ff"
	r.conn.dialErr = errString("connection refused")
	r.a.quickAddr = "10.0.0.9"
	r.a.connectQuick()
	r.wait("the failure", func() bool { return r.a.tabs[0].state == tabError })
	if !r.has("connection refused") || !r.has("Retry") || !r.has("Close") {
		t.Fatalf("the failure card: %q", r.texts())
	}
	r.conn.dialErr = nil
	r.pressButton("sess.retry")
	r.wait("the retry to connect", func() bool { return r.a.tabs[0].state == tabLive })
	if r.conn.dials < 2 {
		t.Errorf("dials %d", r.conn.dials)
	}
}

// The host refused the code: Retry must not send the same one again. The card asks for
// another, already cleared and with the keyboard, and Enter sends what is typed there.
func TestARefusedPasswordIsAskedForAgainNotResent(t *testing.T) {
	r := newRig(t)
	r.conn.pinned["10.0.0.5:7000"] = "x"
	r.conn.refuse = &quic.ApplicationError{ErrorCode: remote.CloseBadPassword, Remote: true}
	r.a.quickAddr, r.a.quickPw = "10.0.0.5", "123465"
	r.a.connectQuick()
	r.wait("the refusal", func() bool { return r.a.tabs[0].state == tabError })
	if !r.has("Wrong password or pairing code.") || !r.has("Password or pairing code") || !r.has("A pairing code works once") {
		t.Fatalf("the card does not ask for another password: %q", r.texts())
	}
	if r.a.tabs[0].password != "" {
		t.Errorf("the refused password %q is still there to be sent again", r.a.tabs[0].password)
	}
	field := kit.ID("sess.pw." + itoa(r.a.tabs[0].serial))
	if r.a.ui.Focused() != field {
		t.Fatalf("focus is on %q, not on the password field", r.a.ui.Focused())
	}
	r.conn.refuse = nil
	r.typ("123456")
	r.key(kit.KeyPress{Key: kit.KeyEnter})
	r.wait("the second try to go live", func() bool { return r.a.tabs[0].state == tabLive })
	if got := r.conn.passwords; len(got) != 2 || got[0] != "123465" || got[1] != "123456" {
		t.Errorf("passwords sent %q, want the refused one and then the one typed on the card", got)
	}
}

// Locked out: the password may well be right, so it is kept for the retry after the wait.
// Not paired: the card asks too. A network failure does not.
func TestWhichFailuresAskForAPassword(t *testing.T) {
	cases := []struct {
		err  error
		ask  bool
		keep bool
	}{
		{&quic.ApplicationError{ErrorCode: remote.CloseLocked, Remote: true}, true, true},
		{&quic.ApplicationError{ErrorCode: remote.CloseUntrusted, Remote: true}, true, true},
		{&quic.ApplicationError{ErrorCode: remote.CloseRevoked, Remote: true}, true, true},
		{&quic.ApplicationError{ErrorCode: remote.CloseKicked, Remote: true}, false, true},
		{errString("connection refused"), false, true},
	}
	for _, c := range cases {
		r := newRig(t)
		r.conn.pinned["10.0.0.6:7000"] = "x"
		if _, ok := c.err.(*quic.ApplicationError); ok {
			r.conn.refuse = c.err
		} else {
			r.conn.dialErr = c.err
		}
		r.a.openTab("", "10.0.0.6", "s3cret", "")
		r.wait("the failure", func() bool { return r.a.tabs[0].state == tabError })
		if got := r.has("Password or pairing code"); got != c.ask {
			t.Errorf("%v: asks for a password %v, want %v", c.err, got, c.ask)
		}
		if got := r.a.tabs[0].password == "s3cret"; got != c.keep {
			t.Errorf("%v: password kept %v, want %v", c.err, got, c.keep)
		}
	}
}

// A password field shows what was typed only when asked, and never through the input method.
func TestPasswordFieldsCanShowWhatWasTyped(t *testing.T) {
	r := newRig(t)
	r.a.ui.Focus("quick.pw", true)
	r.typ("123 456")
	if r.has("123 456") {
		t.Fatal("the password is on screen before Show was pressed")
	}
	r.pressButton("quick.pw/show")
	if !r.has("123 456") || !r.has("Hide") {
		t.Fatalf("Show did not show it: %q", r.texts())
	}
	r.a.ui.Focus("quick.pw", true)
	r.paint()
	if _, wanted := r.a.ui.IME(); wanted {
		t.Error("a password field that is showing its text asked for the input method")
	}
	r.pressButton("quick.pw/show")
	if r.has("123 456") {
		t.Error("Hide did not hide it")
	}
	// The host's own password has the same button.
	r.a.ShowPage(PageThisPC)
	r.step(kit.Input{})
	if !r.has("Show") {
		t.Errorf("no Show button on This PC: %q", r.texts())
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestHostIdentityChangeCanOnlyBeAcceptedByForgettingTheOldOne(t *testing.T) {
	r := newRig(t)
	r.conn.pinned["10.0.0.9:7000"] = "old"
	r.conn.dialErr = errString("host identity changed")
	r.a.quickAddr = "10.0.0.9"
	r.a.connectQuick()
	r.wait("the failure", func() bool { return r.a.tabs[0].state == tabError })
	if !r.a.tabs[0].hostChanged || !r.has("Forget the saved identity") {
		t.Fatalf("no way out offered: %q", r.texts())
	}
	r.pressButton("sess.forget")
	if len(r.conn.forgot) != 1 || r.a.tabs[0].hostChanged {
		t.Errorf("forgot %v, hostChanged %v", r.conn.forgot, r.a.tabs[0].hostChanged)
	}
	if !r.has("Forgot the saved identity of this host") {
		t.Errorf("no confirmation: %q", r.texts())
	}
}

func TestSavingEditingPinningAndDeletingDevices(t *testing.T) {
	r := newRig(t)
	r.a.quickAddr, r.a.quickPw = "192.168.1.20", "pw"
	r.pressButton("quick.save")
	if r.a.dialog == nil {
		t.Fatal("no dialog")
	}
	r.step(kit.Input{})
	if !r.has("Add device") {
		t.Fatalf("dialog: %q", r.texts())
	}
	r.a.ui.Focus("dlg.name", true)
	r.typ("Workshop 1")
	r.pressButton("dlg.save")
	if r.a.dialog != nil {
		t.Fatal("the dialog is still open after Save")
	}
	items := r.store.All()
	if len(items) != 1 || items[0].Name != "Workshop 1" || items[0].Addr != "192.168.1.20" || items[0].Password != "pw" {
		t.Fatalf("saved %+v", items)
	}
	if !r.has("Workshop 1") || !r.has("Never connected") {
		t.Errorf("the card is not showing: %q", r.texts())
	}
	// Pin it, then edit its name.
	r.pressButton("dev.0/pin")
	if !r.store.All()[0].Pinned {
		t.Error("not pinned")
	}
	r.pressButton("dev.0/edit")
	r.a.ui.Focus("dlg.name", true)
	r.key(kit.KeyPress{Key: kit.KeyA, Ctrl: true})
	r.typ("Line 1")
	r.pressButton("dlg.save")
	if got := r.store.All(); len(got) != 1 || got[0].Name != "Line 1" || !got[0].Pinned {
		t.Fatalf("after editing %+v: the pin must survive an edit", got)
	}
	// Connect from the card: the device is touched and listed with its last use.
	r.conn.pinned["192.168.1.20:7000"] = "x"
	r.pressButton("dev.0/connect")
	if got := r.store.All()[0].LastUsed; got != r.now.Unix() && got == 0 {
		t.Errorf("last used = %d", got)
	}
	r.wait("the tab", func() bool { return r.a.tabs[0].state == tabLive })
	r.a.setActive(0)
	r.step(kit.Input{})
	if !r.has("Last connected today 09:30") {
		t.Errorf("last connection not shown: %q", r.texts())
	}
	// Delete asks first.
	r.pressButton("dev.0/delete")
	if len(r.store.All()) != 1 {
		t.Fatal("deleted without asking")
	}
	r.step(kit.Input{})
	if !r.has("Delete this device? Line 1") {
		t.Errorf("no question: %q", r.texts())
	}
	r.pressButton("dlg.delete")
	if len(r.store.All()) != 0 {
		t.Error("not deleted")
	}
	if !r.has("No saved devices yet.") {
		t.Errorf("the empty state is not showing: %q", r.texts())
	}
}

func TestSearchAndSortSavedDevices(t *testing.T) {
	r := newRig(t)
	for i, n := range []string{"Alpha", "Bravo", "Charlie"} {
		r.store.Upsert(profiles.Profile{Name: n, Addr: "10.0.0." + itoa(i+1), LastUsed: int64(100 - i*10)})
	}
	r.a.ui.Invalidate()
	r.step(kit.Input{})
	order := func() []string {
		var out []string
		for _, s := range r.texts() {
			if s == "Alpha" || s == "Bravo" || s == "Charlie" {
				out = append(out, s)
			}
		}
		return out
	}
	if got := strings.Join(order(), ","); got != "Alpha,Bravo,Charlie" {
		t.Errorf("by recent use: %s", got)
	}
	r.a.ui.Focus("dev.search", false)
	r.typ("rav")
	if got := strings.Join(order(), ","); got != "Bravo" {
		t.Errorf("searching rav: %s", got)
	}
	r.typ("zz")
	if !r.has("No device matches: ravzz") {
		t.Errorf("no empty-search message: %q", r.texts())
	}
}

func TestThisPCPageModeSwitchAsksBeforeOpeningUp(t *testing.T) {
	r := newRig(t)
	r.a.page = PageThisPC
	r.a.ui.Invalidate()
	r.step(kit.Input{})
	if !r.has("Strict: a new device needs pairing or the password.") {
		t.Fatalf("strict not described: %q", r.texts())
	}
	if r.has("Open mode: anyone can control this PC") {
		t.Error("the open-mode banner shows in strict mode")
	}
	r.a.ui.Focus("pc.mode/a", true)
	r.key(kit.KeyPress{Key: kit.KeyRight}) // chooses Open
	if r.host.called("open") {
		t.Fatal("went open without asking")
	}
	r.step(kit.Input{})
	if !r.has("Switch to open mode?") {
		t.Fatalf("no confirmation: %q", r.texts())
	}
	r.pressButton("dlg.cancel")
	if r.host.called("open") || !r.host.info.RequireAuth {
		t.Error("cancel opened the host")
	}
	r.a.ui.Focus("pc.mode/a", true)
	r.key(kit.KeyPress{Key: kit.KeyRight})
	r.step(kit.Input{})
	r.pressButton("dlg.open.yes")
	if !r.host.called("open") || r.host.info.RequireAuth {
		t.Fatal("confirming did not open the host")
	}
	r.step(kit.Input{})
	if !r.has("Open mode: anyone can control this PC") {
		t.Errorf("the banner is missing in open mode: %q", r.texts())
	}
	// The banner's button goes back to strict at once and says what it ended.
	r.host.endedOnStrict = 2
	r.pressButton("banner.strict")
	if !r.host.info.RequireAuth || !r.has("Strict mode. Ended 2 sessions that were not verified") {
		t.Errorf("strict again: %v %q", r.host.info.RequireAuth, r.texts())
	}
	r.step(kit.Input{})
	if r.has("Open mode: anyone can control this PC") {
		t.Error("the banner stayed after going strict")
	}
}

func TestPairingCodeCountdownAndCancel(t *testing.T) {
	r := newRig(t)
	r.a.page = PageThisPC
	r.a.ui.Invalidate()
	r.step(kit.Input{})
	r.pressButton("pc.pair.start")
	r.step(kit.Input{})
	if !r.host.called("pair") || !r.has("2:00 left") || !r.has("On the new device, enter this code as the password.") {
		t.Fatalf("pairing not showing: %q", r.texts())
	}
	shown := strings.Join(r.texts(), "")
	if !strings.Contains(shown, "123456") {
		t.Errorf("the code is not on screen: %q", r.texts())
	}
	r.host.mu.Lock()
	r.host.info.PairingLeft = 61*time.Second + 100*time.Millisecond
	r.host.mu.Unlock()
	r.a.ui.Invalidate()
	r.step(kit.Input{})
	if !r.has("1:02 left") {
		t.Errorf("countdown: %q", r.texts())
	}
	if w := r.a.NextWake(); w.IsZero() || w.Sub(r.now) > time.Second {
		t.Errorf("a counting-down code needs a frame within a second, wake = %v", w.Sub(r.now))
	}
	r.pressButton("pc.pair.cancel")
	if !r.host.called("close-pairing") {
		t.Error("pairing was not cancelled")
	}
}

func TestPairedDevicesAreForgottenOneByOneAfterAsking(t *testing.T) {
	r := newRig(t)
	r.host.paired = []PairedDevice{{"fp1", "10.0.0.7", time.Date(2026, 10, 27, 18, 2, 0, 0, time.UTC)}, {"fp2", "10.0.0.9", time.Time{}}}
	r.host.info.Paired = 2
	r.a.page = PageThisPC
	r.a.ui.Invalidate()
	r.step(kit.Input{})
	if !r.has("Paired devices (2)") || !r.has("paired 2026-10-27 18:02") {
		t.Fatalf("list: %q", r.texts())
	}
	r.pressButton("pc.forget.0")
	if len(r.host.paired) != 2 {
		t.Fatal("forgot without asking")
	}
	r.step(kit.Input{})
	r.pressButton("dlg.forget.yes")
	if len(r.host.paired) != 1 || r.host.paired[0].Fingerprint != "fp2" {
		t.Errorf("paired = %+v", r.host.paired)
	}
	if !r.has("Forgot 1 paired device") {
		t.Errorf("no notice: %q", r.texts())
	}
	r.pressButton("pc.forget.all")
	r.step(kit.Input{})
	if !r.has("Forget the paired device?") {
		t.Errorf("the singular question is missing: %q", r.texts())
	}
	r.pressButton("dlg.forget.yes")
	if len(r.host.paired) != 0 {
		t.Error("not all forgotten")
	}
}

func TestControllerCardAndDisconnect(t *testing.T) {
	r := newRig(t)
	r.host.info.Controller, r.host.info.ControllerVerified = "10.0.0.7:51822", true
	r.host.info.Rejected, r.host.info.RejectedWhy = "10.0.0.55:50122", ReasonNotPaired
	r.a.page = PageThisPC
	r.a.ui.Invalidate()
	r.step(kit.Input{})
	if !r.has("10.0.0.7:51822") || !r.has("Verified") || !r.has("Last refused: 10.0.0.55:50122 (not paired)") {
		t.Fatalf("controller card: %q", r.texts())
	}
	r.pressButton("pc.kick")
	if !r.host.called("kick") {
		t.Error("not disconnected")
	}
	r.host.info.Controller = ""
	r.a.ui.Invalidate()
	r.step(kit.Input{})
	if !r.has("Nobody is controlling this PC.") {
		t.Errorf("empty state: %q", r.texts())
	}
}

func TestHostPasswordIsSavedAsItIsTyped(t *testing.T) {
	r := newRig(t)
	r.a.page = PageThisPC
	r.a.ui.Invalidate()
	r.step(kit.Input{})
	r.a.ui.Focus("pc.password", false)
	r.typ("hunter2")
	if r.host.password != "hunter2" {
		t.Errorf("host password = %q", r.host.password)
	}
}

func TestSharingUnavailableIsExplainedAndHidesTheRest(t *testing.T) {
	r := newRig(t)
	r.host.info.Err = "UDP port 7000 is already in use"
	r.a.page = PageThisPC
	r.a.ui.Invalidate()
	r.step(kit.Input{})
	if !r.has("Sharing is unavailable: UDP port 7000 is already in use") {
		t.Fatalf("%q", r.texts())
	}
	if r.has("Security") || r.has("Pair a new device") {
		t.Error("controls for a host that is not running are showing")
	}
	if r.has("Open mode: anyone can control this PC") {
		t.Error("an open-mode banner for a host that is not running")
	}
}

func TestSwitchingLanguageAndThemeIsImmediate(t *testing.T) {
	r := newRig(t)
	r.a.page = PageSettings
	r.a.ui.Invalidate()
	r.step(kit.Input{})
	if !r.has("Appearance") || !r.has("Language") {
		t.Fatalf("settings: %q", r.texts())
	}
	r.a.ui.Focus("set.lang/a", true)
	r.key(kit.KeyPress{Key: kit.KeyRight}) // 简体中文
	if r.a.Lang() != i18n.Chinese || !r.has("外观") || r.has("Appearance") {
		t.Errorf("Chinese: lang %v %q", r.a.Lang(), r.texts())
	}
	if v, _ := r.set.Get(keyLang); v != "zh-Hans" {
		t.Errorf("stored language %q", v)
	}
	r.a.ui.Focus("set.lang/b", true)
	r.key(kit.KeyPress{Key: kit.KeyRight}, kit.KeyPress{Key: kit.KeyRight}) // English, 한국어
	if r.a.Lang() != i18n.Korean || !r.has("모양") {
		t.Errorf("Korean: %v %q", r.a.Lang(), r.texts())
	}
	darkBefore := r.a.Dark()
	r.a.ui.Focus("set.theme/a", true)
	r.key(kit.KeyPress{Key: kit.KeyRight, Ctrl: false}, kit.KeyPress{Key: kit.KeyRight}) // dark, light
	if r.a.Dark() || !darkBefore {
		t.Errorf("light theme: dark = %v (was %v)", r.a.Dark(), darkBefore)
	}
	if len(r.plat.themeSet) == 0 || r.plat.themeSet[len(r.plat.themeSet)-1] {
		t.Errorf("the title bar was not told: %v", r.plat.themeSet)
	}
}

func TestLanguageFollowsTheSystemUnlessChosen(t *testing.T) {
	for _, c := range []struct {
		langs  []string
		stored string
		want   i18n.Lang
	}{
		{[]string{"ko-KR", "en-US"}, "", i18n.Korean},
		{[]string{"zh-CN"}, "auto", i18n.Chinese},
		{[]string{"fr-FR"}, "", i18n.English},
		{[]string{"ko-KR"}, "en", i18n.English},
		{[]string{"en-US"}, "zh-Hans", i18n.Chinese},
		{[]string{"en-US"}, "nonsense", i18n.English},
	} {
		opts := []rigOption{func(r *rig) { r.plat.langs = c.langs }}
		if c.stored != "" {
			opts = append(opts, withLang(c.stored))
		}
		if r := newRig(t, opts...); r.a.Lang() != c.want {
			t.Errorf("system %v stored %q: language %v, want %v", c.langs, c.stored, r.a.Lang(), c.want)
		}
	}
}

func TestSystemThemeIsFollowedByDefault(t *testing.T) {
	if r := newRig(t, func(r *rig) { r.plat.dark, r.plat.darkOK = false, true }); r.a.Dark() {
		t.Error("the system is light but the app is dark")
	}
	if r := newRig(t, func(r *rig) { r.plat.dark, r.plat.darkOK = false, false }); !r.a.Dark() {
		t.Error("when the system theme cannot be read the app should be dark")
	}
	if r := newRig(t, withTheme("dark"), func(r *rig) { r.plat.dark, r.plat.darkOK = false, true }); !r.a.Dark() {
		t.Error("an explicit choice must win over the system")
	}
}

func TestUpdateButtonTakesTwoClicksToRestart(t *testing.T) {
	r := newRig(t, withUpdater())
	if !r.has("Check for updates") {
		t.Fatalf("sidebar: %q", r.texts())
	}
	r.pressButton("nav.update")
	if r.upd.clicks != 1 {
		t.Fatalf("a check was not requested: %d", r.upd.clicks)
	}
	r.upd.set(update.Status{State: update.Ready, Version: "0.8.0"}, true)
	r.a.ui.Invalidate()
	r.step(kit.Input{})
	if !r.has("Restart to update to v0.8.0") {
		t.Fatalf("ready label: %q", r.texts())
	}
	r.pressButton("nav.update")
	if r.upd.clicks != 1 {
		t.Fatal("restarted on the first click: it ends every session")
	}
	r.step(kit.Input{})
	if !r.has("Click again to restart now") {
		t.Errorf("no confirmation text: %q", r.texts())
	}
	r.pressButton("nav.update")
	if r.upd.clicks != 2 {
		t.Errorf("the second click did not restart (%d)", r.upd.clicks)
	}
	// The first click is forgotten after a while.
	r.a.updateArmedUntil = r.now.Add(armFor)
	for i := 0; i < 300; i++ {
		r.step(kit.Input{})
	}
	if r.a.updateArmed() {
		t.Error("the first click stayed armed for ever")
	}
}

func TestQuitAsksOnlyWhenSomethingWouldEnd(t *testing.T) {
	r := newRig(t)
	r.pressButton("nav.quit")
	if !r.plat.quit {
		t.Error("quitting with nothing open should be immediate")
	}
	r = newRig(t)
	r.host.info.Controller = "10.0.0.7:5555"
	r.pressButton("nav.quit")
	if r.plat.quit {
		t.Fatal("quit with a controller connected, without asking")
	}
	r.step(kit.Input{})
	if !r.has("Quit go-nx? Sharing stops and every open connection ends.") {
		t.Errorf("no question: %q", r.texts())
	}
	r.pressButton("dlg.quit.yes")
	if !r.plat.quit {
		t.Error("confirming did not quit")
	}
}

func TestLogsPageShowsTheEndAndCopiesEverything(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 200; i++ {
		r.logs.Write([]byte("log line " + itoa(i) + "\n"))
	}
	r.a.page = PageLogs
	r.a.ui.Invalidate()
	r.step(kit.Input{})
	r.step(kit.Input{})
	if !r.has("log line 199") || r.has("log line 0") {
		t.Errorf("the log should be scrolled to its newest lines; showing %d strings", len(r.texts()))
	}
	// Only the lines in view are drawn, though there are 200.
	drawn := 0
	for _, s := range r.texts() {
		if strings.HasPrefix(s, "log line ") {
			drawn++
		}
	}
	if drawn == 0 || drawn >= 200 {
		t.Errorf("%d of 200 log lines drawn: a long log must be drawn only where it is seen", drawn)
	}
	r.pressButton("logs.copy")
	if !strings.HasPrefix(r.plat.clip.s, "log line 0\nlog line 1\n") || !strings.HasSuffix(r.plat.clip.s, "log line 199") {
		t.Errorf("copied %q...", r.plat.clip.s[:min(40, len(r.plat.clip.s))])
	}
	r.logs.Write([]byte("log line 200\n"))
	r.a.ui.Invalidate()
	r.step(kit.Input{})
	r.step(kit.Input{})
	if !r.has("log line 200") {
		t.Error("a new line did not scroll into view for a reader at the end")
	}
}

func TestDiagnosticsHaveNoPasswordsAndTheLastLines(t *testing.T) {
	r := newRig(t)
	r.host.password = "hunter2-secret"
	r.a.quickPw = "pairing-secret"
	for i := 0; i < 80; i++ {
		r.logs.Write([]byte("line " + itoa(i) + "\n"))
	}
	d := r.a.Diagnostics()
	if strings.Contains(d, "hunter2-secret") || strings.Contains(d, "pairing-secret") {
		t.Error("diagnostics contain a password")
	}
	if !strings.Contains(d, "go-nx 0.7.0") || !strings.Contains(d, "line 79") || strings.Contains(d, "line 29\n") || !strings.Contains(d, "line 30\n") {
		t.Errorf("diagnostics:\n%s", d)
	}
	r.a.page = PageSettings
	r.a.ui.Invalidate()
	r.step(kit.Input{})
	r.pressButton("set.diag")
	if r.plat.clip.s != d && !strings.HasPrefix(r.plat.clip.s, "go-nx 0.7.0") {
		t.Errorf("clipboard holds %q", r.plat.clip.s)
	}
}

func TestTabsCanBeRenamedAndClosed(t *testing.T) {
	r := newRig(t)
	r.conn.pinned["10.0.0.1:7000"], r.conn.pinned["10.0.0.2:7000"] = "x", "y"
	r.store.Upsert(profiles.Profile{Name: "One", Addr: "10.0.0.1"})
	r.a.openTab("One", "10.0.0.1", "", "One")
	r.a.openTab("", "10.0.0.2", "", "")
	r.wait("both tabs", func() bool { return r.a.tabs[0].state == tabLive && r.a.tabs[1].state == tabLive })
	if r.a.active != 2 || !r.has("10.0.0.2:7000") || !r.has("One") {
		t.Errorf("tab strip: %q (active %d)", r.texts(), r.a.active)
	}
	r.a.cycleTab(-1)
	if r.a.active != 1 {
		t.Errorf("previous tab: %d", r.a.active)
	}
	r.a.openRenameDialog(r.a.tabs[0])
	r.step(kit.Input{})
	r.a.ui.Focus("dlg.name", true)
	r.key(kit.KeyPress{Key: kit.KeyA, Ctrl: true})
	r.typ("Press 4")
	r.pressButton("dlg.save")
	if r.a.tabs[0].title != "Press 4" || r.store.All()[0].Name != "Press 4" {
		t.Errorf("renamed to %q / %q: the saved device is renamed with its tab", r.a.tabs[0].title, r.store.All()[0].Name)
	}
	r.a.closeTab(1)
	if len(r.a.tabs) != 1 || !r.conn.conns[0].closed {
		t.Errorf("tabs %d, first connection closed: %v", len(r.a.tabs), r.conn.conns[0].closed)
	}
	if r.a.active != 0 && r.a.active != 1 {
		t.Errorf("active %d", r.a.active)
	}
}

func TestSessionEndingMovesTheTabToAnErrorCard(t *testing.T) {
	r := newRig(t)
	r.conn.pinned["10.0.0.1:7000"] = "x"
	r.a.openTab("", "10.0.0.1", "", "")
	r.wait("live", func() bool { return r.a.tabs[0].state == tabLive })
	r.conn.conns[0].Close()
	r.wait("the end to be noticed", func() bool { return r.a.tabs[0].state == tabError })
	if !r.has("Disconnected") || !r.has("Retry") {
		t.Errorf("%q", r.texts())
	}
}

func TestTheTrayAndOtherGoroutinesHandWorkToTheLoop(t *testing.T) {
	r := newRig(t)
	done := make(chan struct{})
	go func() {
		r.a.Do(func() { r.a.ShowPage(PageSettings) })
		r.a.Do(func() { r.a.Toast("hello", kit.ToneGood) })
		close(done)
	}()
	<-done
	r.step(kit.Input{})
	if r.a.page != PageSettings || !r.has("hello") {
		t.Errorf("page %v, texts %q: both pieces of work should have run, in order", r.a.page, r.texts())
	}
}

func TestToastsExpire(t *testing.T) {
	r := newRig(t)
	r.a.Toast("toast-text-42", kit.ToneGood)
	r.step(kit.Input{})
	if !r.has("toast-text-42") {
		t.Fatal("no toast")
	}
	if w := r.a.NextWake(); w.IsZero() {
		t.Error("a toast that expires by itself needs a wake-up")
	}
	for i := 0; i < 220; i++ { // 4.4 s at 20 ms
		r.step(kit.Input{})
	}
	if r.has("toast-text-42") {
		t.Error("the toast stayed")
	}
}

// Things nobody tells the application about (the updater's progress, a new line in the log)
// are noticed by the poll, which asks for a frame only when one of them changed.
func TestThePollNoticesWhatNobodyAnnounces(t *testing.T) {
	r := newRig(t, withUpdater())
	stop := make(chan struct{})
	defer close(stop)
	r.a.page = PageLogs
	r.step(kit.Input{}) // publishes the page the poll reads
	r.a.StartPolling(stop)
	time.Sleep(100 * time.Millisecond) // let it take its first look before anything changes
	wait := func(what string, f func()) {
		t.Helper()
		r.a.dirty.Store(false)
		f()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if r.a.dirty.Load() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Errorf("no frame was asked for after %s", what)
	}
	wait("the download made progress", func() { r.upd.set(update.Status{State: update.Downloading, Version: "0.8.0", Percent: 40}, false) })
	wait("a line was logged while the log is showing", func() { r.logs.Write([]byte("something new\n")) })
	// Quiet: nothing changes, nothing is asked for.
	r.a.dirty.Store(false)
	time.Sleep(3 * pollEvery)
	if r.a.dirty.Load() {
		t.Error("a frame was asked for although nothing changed")
	}
	// A log line while another page is showing costs nothing.
	r.a.page = PageDevices
	r.step(kit.Input{})
	time.Sleep(2 * pollEvery)
	r.a.dirty.Store(false)
	r.logs.Write([]byte("unseen\n"))
	time.Sleep(3 * pollEvery)
	if r.a.dirty.Load() {
		t.Error("a log line woke the window although the log is not showing")
	}
}
