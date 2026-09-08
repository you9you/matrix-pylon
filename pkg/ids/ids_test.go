package ids

import (
	"testing"

	"github.com/duo/matrix-pylon/pkg/onebot"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestMakeMessageID(t *testing.T) {
	got := MakeMessageID("123456", "789012")
	want := networkid.MessageID("123456:789012")
	if got != want {
		t.Errorf("MakeMessageID = %q, want %q", got, want)
	}
}

func TestMakeFakeMessageID(t *testing.T) {
	got := MakeFakeMessageID("123456", "timestamp123")
	want := networkid.MessageID("fake:123456:timestamp123")
	if got != want {
		t.Errorf("MakeFakeMessageID = %q, want %q", got, want)
	}
}

func TestParseMessageID(t *testing.T) {
	tests := []struct {
		name    string
		id      networkid.MessageID
		peerID  string
		msgID   string
		wantErr bool
	}{
		{"valid message", "123456:789012", "123456", "789012", false},
		{"valid with colon in msgID", "123456:789012:extra", "123456", "789012:extra", false},
		{"missing separator", "invalid", "", "", true},
		{"empty id", "", "", "", true},
		{"fake message rejected", "fake:123:456", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			peerID, msgID, err := ParseMessageID(tt.id)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseMessageID(%q) expected error, got nil", tt.id)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseMessageID(%q) unexpected error: %v", tt.id, err)
			}
			if peerID != tt.peerID {
				t.Errorf("peerID = %q, want %q", peerID, tt.peerID)
			}
			if msgID != tt.msgID {
				t.Errorf("msgID = %q, want %q", msgID, tt.msgID)
			}
		})
	}
}

func TestParseMessageIDRoundTrip(t *testing.T) {
	for _, raw := range []string{"111:222", "333:444"} {
		t.Run(raw, func(t *testing.T) {
			peerID, msgID, err := ParseMessageID(networkid.MessageID(raw))
			if err != nil {
				t.Fatalf("ParseMessageID(%q) error: %v", raw, err)
			}
			roundTripped := MakeMessageID(peerID, msgID)
			if roundTripped != networkid.MessageID(raw) {
				t.Errorf("round trip failed: got %q, want %q", roundTripped, raw)
			}
		})
	}
}

func TestParsePortalID(t *testing.T) {
	tests := []struct {
		name     string
		portalID networkid.PortalID
		wantType PeerType
		wantID   string
	}{
		{"group portal", "group123456", PeerTypeGroup, "123456"},
		{"user portal (no separator)", "42", PeerTypeUser, "42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			peerType, peerID := ParsePortalID(tt.portalID)
			if peerType != tt.wantType {
				t.Errorf("peerType = %q, want %q", peerType, tt.wantType)
			}
			if peerID != tt.wantID {
				t.Errorf("peerID = %q, want %q", peerID, tt.wantID)
			}
		})
	}
}

func TestGetPeerID_PrivateFromOther(t *testing.T) {
	// 别人发来的私聊消息 → peerID = Sender.UserID
	msg := &onebot.Message{
		MessageType: "private",
		Sender:      onebot.Sender{UserID: "42"},
	}
	peerID := GetPeerID(msg)
	if peerID != "42" {
		t.Errorf("GetPeerID = %q, want %q", peerID, "42")
	}
}

func TestGetPeerID_PrivateSentBySelf(t *testing.T) {
	// 自己发出去的私聊消息 → peerID = TargetID
	msg := &onebot.Message{
		MessageType: "private",
		Sender:      onebot.Sender{UserID: "111"},
		TargetID:    "99",
		// PostType set via embedded Event
	}
	msg.PostType = "message_sent"
	peerID := GetPeerID(msg)
	if peerID != "99" {
		t.Errorf("GetPeerID = %q, want %q", peerID, "99")
	}
}

func TestGetPeerID_Group(t *testing.T) {
	// 群消息 → peerID = GroupID
	msg := &onebot.Message{
		MessageType: "group",
		GroupID:     "555555",
	}
	peerID := GetPeerID(msg)
	if peerID != "555555" {
		t.Errorf("GetPeerID = %q, want %q", peerID, "555555")
	}
}

func TestMakeUserID(t *testing.T) {
	if got := MakeUserID("42"); got != networkid.UserID("42") {
		t.Errorf("MakeUserID = %q, want %q", got, "42")
	}
}

func TestMakeUserLoginID(t *testing.T) {
	if got := MakeUserLoginID("42"); got != networkid.UserLoginID("42") {
		t.Errorf("MakeUserLoginID = %q, want %q", got, "42")
	}
}
