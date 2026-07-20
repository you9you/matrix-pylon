package connector

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/duo/matrix-pylon/pkg/onebot"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
)

type resyncQueueItem struct {
	portal *bridgev2.Portal
	ghost  *bridgev2.Ghost
}

type PylonClient struct {
	main      *PylonConnector
	userLogin *bridgev2.UserLogin
	client    *onebot.Client

	stopLoops       atomic.Pointer[context.CancelFunc]
	resyncQueue     map[string]resyncQueueItem
	resyncQueueLock sync.Mutex
	nextResync      time.Time
}

var (
	_ bridgev2.NetworkAPI                    = (*PylonClient)(nil)
	_ bridgev2.IdentifierResolvingNetworkAPI = (*PylonClient)(nil)
	_ bridgev2.RedactionHandlingNetworkAPI   = (*PylonClient)(nil)
)

func (pc *PylonClient) Connect(ctx context.Context) {
	if pc.client == nil {
		state := status.BridgeState{
			StateEvent: status.StateBadCredentials,
			Message:    "You're not logged into Pylon",
		}
		pc.userLogin.BridgeState.Send(state)
		return
	}

	pc.client.SetEventHandler(pc.handleOnebotEvent)

	pc.startLoops()
}

func (pc *PylonClient) Disconnect() {
	// Stop sync
	if stopSyncLoop := pc.stopLoops.Swap(nil); stopSyncLoop != nil {
		(*stopSyncLoop)()
	}

	pc.client.Release()
}

func (pc *PylonClient) LogoutRemote(ctx context.Context) {
	pc.Disconnect()
}

func (pc *PylonClient) IsLoggedIn() bool {
	return pc.client != nil && pc.client.IsLoggedIn()
}

func (pc *PylonClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	return networkid.UserLoginID(userID) == pc.userLogin.ID
}

func (pc *PylonClient) startLoops() {
	ctx, cancel := context.WithCancel(context.Background())
	oldStop := pc.stopLoops.Swap(&cancel)
	if oldStop != nil {
		(*oldStop)()
	}

	go pc.ghostResyncLoop(ctx)
}
