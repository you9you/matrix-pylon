package connector

import (
	"context"

	"github.com/duo/matrix-pylon/pkg/msgconv"
	"github.com/duo/matrix-pylon/pkg/onebot"

	"maunium.net/go/mautrix/bridgev2"
)

var (
	_ bridgev2.NetworkConnector      = (*PylonConnector)(nil)
	_ bridgev2.MaxFileSizeingNetwork = (*PylonConnector)(nil)
	_ bridgev2.StoppableNetwork      = (*PylonConnector)(nil)
)

type PylonConnector struct {
	Bridge  *bridgev2.Bridge
	Config  Config
	MsgConv *msgconv.MessageConverter
	Service *onebot.Service
}

func (pc *PylonConnector) Init(bridge *bridgev2.Bridge) {
	pc.Bridge = bridge
	pc.MsgConv = msgconv.NewMessageConverter(bridge)
	pc.Service = onebot.NewService(
		bridge.Log,
		pc.Config.Onebot.Endpoint,
		pc.Config.Onebot.RequestTimeout,
	)
}

func (pc *PylonConnector) Start(ctx context.Context) error {
	go pc.Service.Start()

	return nil
}

func (pc *PylonConnector) Stop() {
	pc.Service.Stop()
}

func (pc *PylonConnector) SetMaxFileSize(maxSize int64) {
	pc.MsgConv.MaxFileSize = maxSize
}

func (pc *PylonConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:      "Matrix Pylon",
		NetworkURL:       "https://github.com/duo/matrix-pylon",
		NetworkIcon:      "mxc://matrix.org/AwZtDXoKWtgHvcaDAQhbFvfH",
		NetworkID:        "pylon",
		BeeperBridgeType: "pylon",
		DefaultPort:      23457,
	}
}

func (pc *PylonConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	p := &PylonClient{
		main:        pc,
		userLogin:   login,
		resyncQueue: make(map[string]resyncQueueItem),
	}
	login.Client = p

	loginMetadata := login.Metadata.(*UserLoginMetadata)
	if len(loginMetadata.Token) == 0 {
		p.userLogin.Log.Warn().Msg("No token found for user")
	} else {
		loginID := string(login.ID)
		log := p.userLogin.Log.With().Str("user_login_id", loginID).Logger()
		p.client = pc.Service.NewClient(log, loginID, loginMetadata.Token)
	}

	return nil
}
