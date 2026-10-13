package msgconv

import (
	"context"
	"fmt"

	"github.com/duo/matrix-pylon/pkg/onebot"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

type contextKey int

const (
	contextKeyClient contextKey = iota
	contextKeyIntent
	contextKeyPortal
	contextKeyPreferHTTP // forward 消息媒体优先走 http 直链下载
)

func (mc *MessageConverter) parseText(ctx context.Context, content *event.MessageEventContent) (text string, mentions []string) {
	mentions = make([]string, 0)

	parseCtx := format.NewContext(ctx)
	parseCtx.ReturnData["allowed_mentions"] = content.Mentions
	parseCtx.ReturnData["output_mentions"] = &mentions
	if content.Format == event.FormatHTML {
		text = mc.HTMLParser.Parse(content.FormattedBody, parseCtx)
	} else {
		text = content.Body
	}
	return
}

func (mc *MessageConverter) convertPill(displayname, mxid, eventID string, ctx format.Context) string {
	if len(mxid) == 0 || mxid[0] != '@' {
		return format.DefaultPillConverter(displayname, mxid, eventID, ctx)
	}
	allowedMentions, _ := ctx.ReturnData["allowed_mentions"].(*event.Mentions)
	if allowedMentions != nil && !allowedMentions.Has(id.UserID(mxid)) {
		return displayname
	}
	var oid string
	ghost, err := mc.Bridge.GetGhostByMXID(ctx.Ctx, id.UserID(mxid))
	if err != nil {
		zerolog.Ctx(ctx.Ctx).Err(err).Str("mxid", mxid).Msg("Failed to get ghost for mention")
		return displayname
	} else if ghost != nil {
		oid = string(ghost.ID)
	} else if user, err := mc.Bridge.GetExistingUserByMXID(ctx.Ctx, id.UserID(mxid)); err != nil {
		zerolog.Ctx(ctx.Ctx).Err(err).Str("mxid", mxid).Msg("Failed to get user for mention")
		return displayname
	} else if user != nil {
		portal := getPortal(ctx.Ctx)
		login, _, _ := portal.FindPreferredLogin(ctx.Ctx, user, false)
		if login == nil {
			return displayname
		}
		oid = string(login.ID)
	} else {
		return displayname
	}
	mentions := ctx.ReturnData["output_mentions"].(*[]string)
	*mentions = append(*mentions, oid)
	return fmt.Sprintf("@%s", oid)
}

func getClient(ctx context.Context) *onebot.Client {
	return ctx.Value(contextKeyClient).(*onebot.Client)
}

func getIntent(ctx context.Context) bridgev2.MatrixAPI {
	return ctx.Value(contextKeyIntent).(bridgev2.MatrixAPI)
}

func getPortal(ctx context.Context) *bridgev2.Portal {
	return ctx.Value(contextKeyPortal).(*bridgev2.Portal)
}
