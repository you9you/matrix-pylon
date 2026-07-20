package connector

import (
	"cmp"
	"context"
	"fmt"
	"math/rand/v2"
	"net/url"
	"path"
	"time"

	"github.com/duo/matrix-pylon/pkg/ids"
	"github.com/duo/matrix-pylon/pkg/onebot"
	"github.com/duo/matrix-pylon/pkg/util"

	"github.com/rs/zerolog"
	"go.mau.fi/util/jsontime"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
)

const (
	DirectChatTopic = "Pylon direct chat"

	powerDefault    = 0
	powerAdmin      = 50
	powerSuperAdmin = 75

	resyncMinInterval  = 7 * 24 * time.Hour
	resyncLoopInterval = 4 * time.Hour
)

func (pc *PylonClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	if ghost.Name != "" {
		pc.EnqueueGhostResync(ghost)
		return nil, nil
	}

	if info, err := pc.client.GetUserInfo(string(ghost.ID)); err != nil {
		return nil, fmt.Errorf("failed to fetch user %s: %w", ghost.ID, err)
	} else {
		return pc.contactToUserInfo(info), nil
	}
}

func (pc *PylonClient) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	ghost, err := pc.main.Bridge.GetGhostByID(ctx, ids.MakeUserID(identifier))
	if err != nil {
		return nil, fmt.Errorf("failed to get ghost: %w", err)
	}

	return &bridgev2.ResolveIdentifierResponse{
		Ghost:  ghost,
		UserID: ids.MakeUserID(identifier),
		Chat:   &bridgev2.CreateChatResponse{PortalKey: pc.makeDMPortalKey(identifier)},
	}, nil
}

func (pc *PylonClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	peerType, peerID := ids.ParsePortalID(portal.ID)

	switch peerType {
	case ids.PeerTypeUser:
		return pc.getDirectChatInfo(peerID)
	case ids.PeerTypeGroup:
		return pc.getGroupChatInfo(ctx, portal)
	}

	return nil, fmt.Errorf("unsupported chat type %s", peerType)
}

func (pc *PylonClient) getDirectChatInfo(recipient string) (*bridgev2.ChatInfo, error) {
	members := &bridgev2.ChatMemberList{
		IsFull:           true,
		TotalMemberCount: 2,
		OtherUserID:      ids.MakeUserID(recipient),
		PowerLevels:      nil,
	}

	if networkid.UserLoginID(recipient) != pc.userLogin.ID {
		selfEvtSender := pc.selfEventSender()
		members.MemberMap = map[networkid.UserID]bridgev2.ChatMember{
			selfEvtSender.Sender: {EventSender: selfEvtSender},
			members.OtherUserID:  {EventSender: pc.makeEventSender(recipient)},
		}
	} else {
		members.MemberMap = map[networkid.UserID]bridgev2.ChatMember{
			// For chats with self, force-split the members so the user's own ghost is always in the room.
			"":                  {EventSender: bridgev2.EventSender{IsFromMe: true}},
			members.OtherUserID: {EventSender: bridgev2.EventSender{Sender: members.OtherUserID}},
		}
	}

	return &bridgev2.ChatInfo{
		Topic:   ptr.Ptr(DirectChatTopic),
		Members: members,
		Type:    ptr.Ptr(database.RoomTypeDM),
	}, nil
}

func (pc *PylonClient) getGroupChatInfo(_ context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	_, peerID := ids.ParsePortalID(portal.ID)

	groupInfo, err := pc.client.GetGroupInfo(peerID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch group %s: %w", peerID, err)
	}
	membersInfo, err := pc.client.GetGroupMemberList(peerID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch members %s: %w", peerID, err)
	}

	avatarURL := groupInfo.Avatar
	if pc.client.GetAgentType() == onebot.AgentNapCat || pc.client.GetAgentType() == onebot.AgentLLOneBot {
		avatarURL = util.GetGroupAvatarURL(groupInfo.ID)
	}

	wrapped := &bridgev2.ChatInfo{
		Name:   ptr.Ptr(groupInfo.Name),
		Avatar: wrapAvatar(avatarURL),
		Members: &bridgev2.ChatMemberList{
			IsFull:           true,
			TotalMemberCount: len(membersInfo),
			MemberMap:        make(map[networkid.UserID]bridgev2.ChatMember, len(membersInfo)),
			PowerLevels: &bridgev2.PowerLevelOverrides{
				Events: map[event.Type]int{
					event.StateRoomName:   powerDefault,
					event.StateRoomAvatar: powerDefault,
					event.StateTopic:      powerDefault,
					event.EventReaction:   powerDefault,
					event.EventRedaction:  powerDefault,
				},
				EventsDefault: ptr.Ptr(powerDefault),
				StateDefault:  ptr.Ptr(powerAdmin),
			},
		},
		Disappear: &database.DisappearingSetting{Type: database.DisappearingTypeNone},
		Type:      ptr.Ptr(database.RoomTypeDefault),
	}

	for _, m := range membersInfo {
		evtSender := pc.makeEventSender(m.UserID)
		pl := powerDefault
		if m.Role == "owner" {
			pl = powerSuperAdmin
		} else if m.Role == "admin" {
			pl = powerAdmin
		}

		wrapped.Members.MemberMap[evtSender.Sender] = bridgev2.ChatMember{
			EventSender: evtSender,
			Membership:  event.MembershipJoin,
			PowerLevel:  &pl,
		}
	}

	return wrapped, nil
}

func (pc *PylonClient) contactToUserInfo(contact *onebot.UserInfo) *bridgev2.UserInfo {
	avatarURL := contact.Avatar
	if pc.client.GetAgentType() == onebot.AgentNapCat || pc.client.GetAgentType() == onebot.AgentLLOneBot {
		avatarURL = util.GetUserAvatarURL(contact.ID)
	}

	return &bridgev2.UserInfo{
		IsBot:        nil,
		Identifiers:  []string{},
		ExtraUpdates: updateGhostLastSyncAt,
		Name: ptr.Ptr(pc.main.Config.FormatDisplayname(DisplaynameParams{
			Alias: contact.Remark,
			Name:  contact.Nickname,
			ID:    contact.ID,
		})),
		Avatar: wrapAvatar(avatarURL),
	}
}

func (pc *PylonClient) EnqueueGhostResync(ghost *bridgev2.Ghost) {
	if ghost.Metadata.(*GhostMetadata).LastSync.Add(resyncMinInterval).After(time.Now()) {
		return
	}

	id := string(ghost.ID)
	pc.resyncQueueLock.Lock()
	if _, exists := pc.resyncQueue[id]; !exists {
		pc.resyncQueue[id] = resyncQueueItem{ghost: ghost}
		pc.userLogin.Log.Debug().
			Str("id", id).
			Stringer("next_resync_in", time.Until(pc.nextResync)).
			Msg("Enqueued resync for ghost")
	}
	pc.resyncQueueLock.Unlock()
}

func (pc *PylonClient) EnqueuePortalResync(portal *bridgev2.Portal) {
	peerType, _ := ids.ParsePortalID(portal.ID)
	if peerType != ids.PeerTypeGroup || portal.Metadata.(*PortalMetadata).LastSync.Add(resyncMinInterval).After(time.Now()) {
		return
	}

	id := string(portal.ID)
	pc.resyncQueueLock.Lock()
	if _, exists := pc.resyncQueue[id]; !exists {
		pc.resyncQueue[id] = resyncQueueItem{portal: portal}
		pc.userLogin.Log.Debug().
			Str("id", id).
			Stringer("next_resync_in", time.Until(pc.nextResync)).
			Msg("Enqueued resync for portal")
	}
	pc.resyncQueueLock.Unlock()
}

func (pc *PylonClient) ghostResyncLoop(ctx context.Context) {
	log := pc.userLogin.Log.With().Str("action", "ghost resync loop").Logger()
	ctx = log.WithContext(ctx)
	pc.nextResync = time.Now().Add(resyncLoopInterval).Add(-time.Duration(rand.IntN(3600)) * time.Second)
	timer := time.NewTimer(time.Until(pc.nextResync))
	log.Info().Time("first_resync", pc.nextResync).Msg("Ghost resync queue starting")

	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		queue := pc.rotateResyncQueue()
		timer.Reset(time.Until(pc.nextResync))
		if len(queue) > 0 {
			pc.doGhostResync(ctx, queue)
		} else {
			log.Trace().Msg("Nothing in background resync queue")
		}
	}
}

func (pc *PylonClient) rotateResyncQueue() map[string]resyncQueueItem {
	pc.resyncQueueLock.Lock()
	defer pc.resyncQueueLock.Unlock()
	pc.nextResync = time.Now().Add(resyncLoopInterval)
	if len(pc.resyncQueue) == 0 {
		return nil
	}
	queue := pc.resyncQueue
	pc.resyncQueue = make(map[string]resyncQueueItem)
	return queue
}

func (pc *PylonClient) doGhostResync(ctx context.Context, queue map[string]resyncQueueItem) {
	log := zerolog.Ctx(ctx)
	if !pc.IsLoggedIn() {
		log.Warn().Msg("Not logged in, skipping background resyncs")
		return
	}

	log.Debug().Msg("Starting background resyncs")
	defer log.Debug().Msg("Background resyncs finished")

	var ghosts []*bridgev2.Ghost
	var portals []*bridgev2.Portal

	for id, item := range queue {
		var lastSync time.Time
		if item.ghost != nil {
			lastSync = item.ghost.Metadata.(*GhostMetadata).LastSync.Time
		} else if item.portal != nil {
			lastSync = item.portal.Metadata.(*PortalMetadata).LastSync.Time
		}

		if lastSync.Add(resyncMinInterval).After(time.Now()) {
			log.Debug().
				Str("id", id).
				Time("last_sync", lastSync).
				Msg("Not resyncing, last sync was too recent")
			continue
		}

		if item.ghost != nil {
			ghosts = append(ghosts, item.ghost)
		} else if item.portal != nil {
			portals = append(portals, item.portal)
		}
	}

	for _, portal := range portals {
		pc.main.Bridge.QueueRemoteEvent(pc.userLogin, &simplevent.ChatResync{
			EventMeta: simplevent.EventMeta{
				Type: bridgev2.RemoteEventChatResync,
				LogContext: func(c zerolog.Context) zerolog.Context {
					return c.Str("sync_reason", "queue")
				},
				PortalKey: portal.PortalKey,
			},
			GetChatInfoFunc: func(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
				info, err := pc.GetChatInfo(ctx, portal)
				if err == nil {
					info.ExtraUpdates = bridgev2.MergeExtraUpdaters(
						info.ExtraUpdates,
						pc.updateMemberDisplyname,
					)
				}
				return info, err
			},
		})
	}

	for _, ghost := range ghosts {
		id := string(ghost.ID)
		contact, err := pc.client.GetUserInfo(id)
		if err != nil {
			log.Warn().Str("id", id).Msg("Failed to get user info for puppet in background sync")
			continue
		}

		ghost.UpdateInfo(ctx, pc.contactToUserInfo(contact))
	}
}

func (pc *PylonClient) updateMemberDisplyname(ctx context.Context, portal *bridgev2.Portal) bool {
	_, peerID := ids.ParsePortalID(portal.ID)
	if members, err := pc.client.GetGroupMemberList(peerID); err == nil {
		for _, member := range members {
			memberIntent, ok := portal.GetIntentFor(ctx, pc.makeEventSender(member.UserID), pc.userLogin, bridgev2.RemoteEventChatInfoChange)
			if !ok {
				zerolog.Ctx(ctx).Err(err).Msg("Failed to get member info")
				continue
			}

			mxid := memberIntent.GetMXID()

			memberInfo, err := portal.Bridge.Matrix.GetMemberInfo(ctx, portal.MXID, mxid)
			if err != nil {
				zerolog.Ctx(ctx).Err(err).Msg("Failed to get member info")
				continue
			}

			displayName := cmp.Or(member.Card, member.Nickname)
			if memberInfo.Displayname != displayName {
				memberInfo.Displayname = displayName

				var zeroTime time.Time
				_, err = memberIntent.SendState(ctx, portal.MXID, event.StateMember, mxid.String(), &event.Content{
					Parsed: memberInfo,
				}, zeroTime)

				if err != nil {
					zerolog.Ctx(ctx).Err(err).Stringer("user_id", mxid).Msg("Failed to update group displayname")
				}
				zerolog.Ctx(ctx).Debug().Stringer("user_id", mxid).Msgf("Update group displayname to %s", displayName)
			}
		}
	}

	return false
}

func updateGhostLastSyncAt(ctx context.Context, ghost *bridgev2.Ghost) bool {
	meta := ghost.Metadata.(*GhostMetadata)
	forceSave := time.Since(meta.LastSync.Time) > 24*time.Hour
	meta.LastSync = jsontime.UnixNow()
	return forceSave
}

func wrapAvatar(avatarURL string) *bridgev2.Avatar {
	if avatarURL == "" {
		return &bridgev2.Avatar{Remove: true}
	}
	parsedURL, _ := url.Parse(avatarURL)
	avatarID := path.Base(parsedURL.Path)
	return &bridgev2.Avatar{
		ID: networkid.AvatarID(avatarID),
		Get: func(ctx context.Context) ([]byte, error) {
			return util.GetBytes(avatarURL)
		},
	}
}
