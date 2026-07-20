package connector

import (
	"context"
	"fmt"
	"time"

	"github.com/duo/matrix-pylon/pkg/ids"
	"github.com/duo/matrix-pylon/pkg/onebot"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

const (
	LoginFlowIDToken = "token"

	LoginStepToken    = "me.lxduo.pylon.login.token"
	LoginStepComplete = "me.lxduo.pylon.login.complete"
)

type TokenLogin struct {
	user   *bridgev2.User
	main   *PylonConnector
	client *onebot.Client
	log    zerolog.Logger
}

var _ bridgev2.LoginProcessDisplayAndWait = (*TokenLogin)(nil)

func (pc *PylonConnector) GetLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{
		{
			Name:        "Access token",
			Description: "Use this token to connect the bridge to your account",
			ID:          LoginFlowIDToken,
		},
	}
}

func (pc *PylonConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if flowID != LoginFlowIDToken {
		return nil, fmt.Errorf("invalid flow ID %s", flowID)
	}

	return &TokenLogin{
		user: user,
		main: pc,
		log: user.Log.With().
			Str("action", "login").
			Stringer("user_id", user.MXID).
			Logger(),
	}, nil
}

func (tl *TokenLogin) Cancel() {
	if tl.client != nil {
		tl.client.Release()
	}
}

func (tl *TokenLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	token := uuid.New().String()

	tl.client = tl.main.Service.NewClient(tl.log, "", token)

	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeDisplayAndWait,
		StepID:       LoginStepToken,
		Instructions: "Use this token to connect the OneBot agent to the bridge",
		DisplayAndWaitParams: &bridgev2.LoginDisplayAndWaitParams{
			Type: bridgev2.LoginDisplayTypeCode,
			Data: token,
		},
	}, nil
}

func (tl *TokenLogin) Wait(ctx context.Context) (*bridgev2.LoginStep, error) {
	if tl.client == nil {
		return nil, fmt.Errorf("login has not started yet")
	}

	zerolog.Ctx(ctx).Debug().Msgf("Start waiting")

	for {
		select {
		case <-ctx.Done():
			tl.Cancel()
			return nil, ctx.Err()
		default:
			if tl.client.IsLoggedIn() {
				if info, err := tl.client.GetLoginInfo(); err == nil {
					tl.client.Release()

					ul, err := tl.user.NewLogin(ctx, &database.UserLogin{
						ID:         ids.MakeUserLoginID(info.ID),
						RemoteName: info.Nickname,
						Metadata:   &UserLoginMetadata{Token: tl.client.GetToken()},
					}, &bridgev2.NewLoginParams{
						DeleteOnConflict: true,
					})
					if err != nil {
						return nil, fmt.Errorf("failed to create user login: %w", err)
					}

					ul.Client.Connect(ul.Log.WithContext(context.Background()))

					return &bridgev2.LoginStep{
						Type:         bridgev2.LoginStepTypeComplete,
						StepID:       LoginStepComplete,
						Instructions: fmt.Sprintf("Successfully logged in as %s", ul.RemoteName),
						CompleteParams: &bridgev2.LoginCompleteParams{
							UserLoginID: ul.ID,
							UserLogin:   ul,
						},
					}, nil
				}
			}
			time.Sleep(3 * time.Second)
		}
	}
}
