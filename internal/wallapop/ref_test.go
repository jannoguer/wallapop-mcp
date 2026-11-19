package wallapop

import "testing"

func TestRefKey(t *testing.T) {
	tests := []struct{ ref, kind, want string }{
		{" ejkml3e23yzx ", "item", "ejkml3e23yzx"},
		{"https://wallapop.com/item/iphone-13-negro-1311621547", "item", "iphone-13-negro-1311621547"},
		{"https://es.wallapop.com/item/iphone-13-1/?_pid=wi", "item", "iphone-13-1"},
		{"https://es.wallapop.com/user/tktkt-460679728#reviews", "user", "tktkt-460679728"},
		{"", "item", ""},
	}
	for _, tt := range tests {
		if got := refKey(tt.ref, tt.kind); got != tt.want {
			t.Errorf("refKey(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}
