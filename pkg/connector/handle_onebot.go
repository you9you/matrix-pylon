package connector

import (
	"context"
	"fmt"
	"time"

	"github.com/duo/matrix-pylon/pkg/ids"
	"github.com/duo/matrix-pylon/pkg/onebot"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func (pc *PylonClient) handleOnebotEvent(evt onebot.IEvent) {
	pc.userLogin.Log.Trace().Msgf("Receive onebot event: %+v", evt)

	switch evt.EventType() {
	case onebot.MessagePrivate, onebot.MessageGroup:
		msg := evt.(*onebot.Message)

		message, ok := msg.Message.([]onebot.ISegment)
		if !ok || len(message) == 0 {
			pc.userLogin.Log.Trace().Bool("ok", ok).Msg("无法断言 msg.Message 或 长度为零")
			return
		}

		pc.main.Bridge.QueueRemoteEvent(pc.userLogin, &OnebotMessageEvent{
			message: msg,
			pc:      pc,
		})
	case onebot.NoticeFriendRecall:
		// TODO: delete message
		friendRecall := evt.(*onebot.FriendRecall)
		// TODO: handle self recall event
		if friendRecall.SelfID == friendRecall.UserID {
			return
		}
		pc.main.Bridge.QueueRemoteEvent(pc.userLogin, &OnebotMessageEvent{
			message: &onebot.Message{
				MessageType: "private",
				MessageID:   friendRecall.MessageID,
				Sender:      onebot.Sender{UserID: friendRecall.UserID},
				Event:       onebot.Event{Time: friendRecall.Time},
				Message: []onebot.ISegment{
					onebot.NewReply(friendRecall.MessageID),
					onebot.NewText("[revoke]"),
				},
			},
			pc:     pc,
			isFake: true,
		})
	case onebot.NoticeGroupRecall:
		// TODO: delete message
		groupRecall := evt.(*onebot.GroupRecall)
		pc.main.Bridge.QueueRemoteEvent(pc.userLogin, &OnebotMessageEvent{
			message: &onebot.Message{
				MessageType: "group",
				MessageID:   groupRecall.MessageID,
				GroupID:     groupRecall.GroupID,
				Sender:      onebot.Sender{UserID: groupRecall.UserID},
				Event:       onebot.Event{Time: groupRecall.Time},
				Message: []onebot.ISegment{
					onebot.NewReply(groupRecall.MessageID),
					onebot.NewText("[revoke]"),
				},
			},
			pc:     pc,
			isFake: true,
		})
	}
}

type OnebotMessageEvent struct {
	message *onebot.Message
	isFake  bool

	pc         *PylonClient
	postHandle func()
}

var (
	_ bridgev2.RemoteEventThatMayCreatePortal = (*OnebotMessageEvent)(nil)
	_ bridgev2.RemoteChatResyncWithInfo       = (*OnebotMessageEvent)(nil)
	_ bridgev2.RemoteMessage                  = (*OnebotMessageEvent)(nil)
	_ bridgev2.RemoteEventWithTimestamp       = (*OnebotMessageEvent)(nil)
	_ bridgev2.RemoteMessageRemove            = (*OnebotMessageEvent)(nil)
	_ bridgev2.RemotePostHandler              = (*OnebotMessageEvent)(nil)
)

func (evt *OnebotMessageEvent) ShouldCreatePortal() bool {
	return true
}

func (evt *OnebotMessageEvent) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str("message_id", evt.message.MessageID).Str("sender_id", evt.message.Sender.UserID)
}

func (evt *OnebotMessageEvent) GetPortalKey() networkid.PortalKey {
	if evt.message.EventType() == onebot.MessagePrivate {
		return evt.pc.makePortalKey(ids.PeerTypeUser, ids.GetPeerID(evt.message))
	} else {
		return evt.pc.makePortalKey(ids.PeerTypeGroup, ids.GetPeerID(evt.message))
	}
}

func (evt *OnebotMessageEvent) GetSender() bridgev2.EventSender {
	return evt.pc.makeEventSender(evt.message.Sender.UserID)
}

func (evt *OnebotMessageEvent) GetID() networkid.MessageID {
	peerID := ids.GetPeerID(evt.message)
	if evt.isFake {
		return ids.MakeFakeMessageID(peerID, fmt.Sprintf("fake-%s", evt.message.MessageID))
	}
	return ids.MakeMessageID(peerID, evt.message.MessageID)
}

func (evt *OnebotMessageEvent) GetTimestamp() time.Time {
	return time.UnixMilli(evt.message.Time * 1000)
}

func (evt *OnebotMessageEvent) GetType() bridgev2.RemoteEventType {
	// TODO:  bridgev2.RemoteEventMessageRemove
	return bridgev2.RemoteEventMessage
}

func (evt *OnebotMessageEvent) GetTargetMessage() networkid.MessageID {
	return ids.MakeMessageID(ids.GetPeerID(evt.message), evt.message.MessageID)
}

func (evt *OnebotMessageEvent) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	if evt.message.EventType() == onebot.MessagePrivate {
		return evt.pc.getDirectChatInfo(string(portal.ID))
	} else {
		if portal.MXID == "" {
			evt.postHandle = func() {
				evt.pc.updateMemberDisplayname(ctx, portal)
			}
		}
		return evt.pc.getGroupChatInfo(ctx, portal)
	}
}

func (evt *OnebotMessageEvent) PostHandle(ctx context.Context, portal *bridgev2.Portal) {
	if ph := evt.postHandle; ph != nil {
		evt.postHandle = nil
		ph()
	}
}

func (evt *OnebotMessageEvent) ConvertMessage(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI) (*bridgev2.ConvertedMessage, error) {
	evt.pc.EnqueuePortalResync(portal)

	return evt.pc.main.MsgConv.OnebotToMatrix(ctx, evt.pc.client, portal, intent, evt.message), nil
}
