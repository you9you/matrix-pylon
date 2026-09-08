package msgconv

import (
	"context"

	"github.com/duo/matrix-pylon/pkg/onebot"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

// testClient returns a Onebot client with no websocket connection.
// Media downloads will fail fast, which is useful for testing failure paths.
func testClient() *onebot.Client {
	svc := onebot.NewService(zerolog.Nop(), "ws://127.0.0.1:1", 0)
	return onebot.NewClient(zerolog.Nop(), "test", "", svc)
}

// testConverter returns a MessageConverter suitable for unit tests.
func testConverter() *MessageConverter {
	return NewMessageConverter(nil)
}

// testPortal returns a minimal Portal for tests.
func testPortal() *bridgev2.Portal {
	return &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: networkid.PortalKey{
				ID:       networkid.PortalID("user123"),
				Receiver: networkid.UserLoginID("user:1"),
			},
			MXID: id.RoomID("!test:example.com"),
		},
	}
}

// testCtx returns a context pre-populated with the converter's expected values.
func testCtx(client *onebot.Client, portal *bridgev2.Portal, intent bridgev2.MatrixAPI) context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, contextKeyClient, client)
	ctx = context.WithValue(ctx, contextKeyPortal, portal)
	if intent != nil {
		ctx = context.WithValue(ctx, contextKeyIntent, intent)
	}
	return ctx
}
