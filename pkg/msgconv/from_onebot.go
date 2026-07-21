package msgconv

import (
	"context"
	"fmt"
	"html"
	"math"
	"path/filepath"
	"strings"
	"sync"

	"github.com/duo/matrix-pylon/pkg/ids"
	"github.com/duo/matrix-pylon/pkg/onebot"

	"github.com/gabriel-vasile/mimetype"
	"github.com/rs/zerolog"
	"github.com/tidwall/gjson"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

// 声明一个切片池，预分配大小为 1024(用于处理Forward消息)
var slicePool = sync.Pool{
	New: func() any {
		s := make([]*bridgev2.ConvertedMessagePart, 0, 1024)
		return &s // 放入指针以避免值拷贝
	},
}

func (mc *MessageConverter) OnebotToMatrix(
	ctx context.Context,
	client *onebot.Client,
	portal *bridgev2.Portal,
	intent bridgev2.MatrixAPI,
	msg *onebot.Message,
) *bridgev2.ConvertedMessage {
	ctx = context.WithValue(ctx, contextKeyClient, client)
	ctx = context.WithValue(ctx, contextKeyIntent, intent)
	ctx = context.WithValue(ctx, contextKeyPortal, portal)

	cm := &bridgev2.ConvertedMessage{}

	var part *bridgev2.ConvertedMessagePart

	mediaParts := make([]*bridgev2.ConvertedMessagePart, 0)
	mentions := make([]string, 0)

	var contentBuilder strings.Builder

	segments := msg.Message.([]onebot.ISegment)
	for _, s := range segments {
		switch v := s.(type) {
		case *onebot.TextSegment:
			fmt.Fprint(&contentBuilder, convertOnebotEmoji(client, v.Content()))
		case *onebot.FaceSegment:
			fmt.Fprint(&contentBuilder, convertOnebotFace(client, v.ID()))
		case *onebot.AtSegment:
			target := v.Target()
			if target == "all" {
				target = "room" // Matrix's mention all
			}
			fmt.Fprintf(&contentBuilder, "@%s", target)
			mentions = append(mentions, target)
		case *onebot.ImageSegment:
			mediaParts = append(mediaParts, mc.convertMediaMessage(ctx, v))
			fmt.Fprint(&contentBuilder, "[Image]")
		case *onebot.MarketFaceSegment:
			mediaParts = append(mediaParts, mc.convertMediaMessage(ctx, v))
			fmt.Fprint(&contentBuilder, "[Image]")
		case *onebot.RecordSegment:

			//BUG: failed to download attachment: failed to download media: &{Segment:{Type:record Data:map[file:<REMOVE>.amr file_size:31028 path:/app/.config/QQ/nt_qq_<REMOVE>/nt_data/Ptt/2026-07/Ori/<REMOVE>.amr url:https://multimedia.nt.qq.com.cn/download?appid=1402&fileid=<REMOVE>&format=amr&rkey=<REMOVE>]}}
			// pkg/onebot/protocol.go:148

			mediaParts = append(mediaParts, mc.convertMediaMessage(ctx, v))
			fmt.Fprint(&contentBuilder, "[Voice]")
		case *onebot.VideoSegment:
			mediaParts = append(mediaParts, mc.convertMediaMessage(ctx, v))
			fmt.Fprint(&contentBuilder, "[Video]")
		case *onebot.FileSegment:
			mediaParts = append(mediaParts, mc.convertMediaMessage(ctx, v))
			fmt.Fprint(&contentBuilder, "[File]")
		case *onebot.ReplySegment:
			cm.ReplyTo = &networkid.MessageOptionalPartID{
				MessageID: ids.MakeMessageID(ids.GetPeerID(msg), v.ID()),
			}
		case *onebot.ForwardSegment:
			//TODO: 实现显示合并消息

			// 从池子中获取一个已经分配好内存的切片指针
			slicePtr := slicePool.Get().(*[]*bridgev2.ConvertedMessagePart)

			// 用完归还，供其他协程复用，极大地减少了 GC 压力
			defer slicePool.Put(slicePtr)

			// 每次使用前，务必清空长度（保留容量）
			*slicePtr = (*slicePtr)[:0]

			err := mc.convertForwardMessage(slicePtr, ctx, client, v)
			if err == nil {
				// 因为要归还池子，这里必须创建一个独立长度的切片并复制数据
				finalParts := make([]*bridgev2.ConvertedMessagePart, len(*slicePtr))
				copy(finalParts, *slicePtr)

				cm.Parts = finalParts
				return cm
			} else {
				fmt.Fprintf(&contentBuilder, "[Chat History]: %s", err)
			}

		case *onebot.ShareSegment:
			part = mc.convertShareMessage(v.Title(), v.Content(), v.URL())
		case *onebot.JSONSegment:
			part = mc.convertJSONMessage(ctx, v)
		default:
			fmt.Fprintf(&contentBuilder, "[%s]", v.SegmentType())
		}
	}

	if part == nil {
		if len(segments) > 1 && len(mediaParts) >= 1 { // mixed image and text
			var imagesMarkdown strings.Builder
			for _, part := range mediaParts {
				fmt.Fprintf(&imagesMarkdown, "![%s](%s)\n", part.Content.FileName, part.Content.URL)
			}

			rendered := format.RenderMarkdown(imagesMarkdown.String(), true, false)
			content := contentBuilder.String()
			part = &bridgev2.ConvertedMessagePart{
				Type: event.EventMessage,
				Content: &event.MessageEventContent{
					MsgType:       event.MsgText,
					Format:        event.FormatHTML,
					Body:          content,
					FormattedBody: fmt.Sprintf("%s\n%s", rendered.FormattedBody, content),
				},
			}
		} else if len(mediaParts) == 1 {
			part = mediaParts[0]
		} else {
			part = &bridgev2.ConvertedMessagePart{
				Type: event.EventMessage,
				Content: &event.MessageEventContent{
					MsgType: event.MsgText,
					Body:    contentBuilder.String(),
				},
			}
		}
	}

	// Mentions
	part.Content.Mentions = &event.Mentions{}
	mc.addMentions(ctx, mentions, part.Content)

	// 确保 part 有 ID
	if part != nil && part.ID == "" {
		part.ID = "0" // 或其他唯一值
	}

	cm.Parts = []*bridgev2.ConvertedMessagePart{part}

	return cm
}

func (mc *MessageConverter) convertMediaMessage(ctx context.Context, seg onebot.ISegment) *bridgev2.ConvertedMessagePart {
	if part, err := mc.reploadAttachment(ctx, seg); err != nil {
		return mc.makeMediaFailure(ctx, err)
	} else {
		return part
	}
}

func (mc *MessageConverter) convertJSONMessage(_ context.Context, seg *onebot.JSONSegment) *bridgev2.ConvertedMessagePart {
	content := seg.Content()

	view := gjson.Get(content, "view").String()
	if view == "LocationShare" {
		name := gjson.Get(content, "meta.*.name").String()
		address := gjson.Get(content, "meta.*.address").String()
		latitude := gjson.Get(content, "meta.*.lat").Float()
		longitude := gjson.Get(content, "meta.*.lng").Float()

		return mc.convertLocationMessage(name, address, latitude, longitude)
	} else {
		if url := gjson.Get(content, "meta.*.qqdocurl").String(); len(url) > 0 {
			desc := gjson.Get(content, "meta.*.desc").String()
			prompt := gjson.Get(content, "prompt").String()
			return mc.convertShareMessage(prompt, desc, url)
		} else if url := gjson.Get(content, "meta.*.jumpUrl").String(); len(url) > 0 {
			desc := gjson.Get(content, "meta.*.desc").String()
			prompt := gjson.Get(content, "prompt").String()
			return mc.convertShareMessage(prompt, desc, url)
		}
	}

	return &bridgev2.ConvertedMessagePart{
		Type: event.EventMessage,
		Content: &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    content,
		},
	}
}

func (mc *MessageConverter) convertLocationMessage(name, address string, latitude, longitude float64) *bridgev2.ConvertedMessagePart {
	url := fmt.Sprintf("https://maps.google.com/?q=%.5f,%.5f", latitude, longitude)
	if len(name) == 0 {
		latChar := 'N'
		if latitude < 0 {
			latChar = 'S'
		}
		longChar := 'E'
		if longitude < 0 {
			longChar = 'W'
		}
		name = fmt.Sprintf("%.4f° %c %.4f° %c", math.Abs(latitude), latChar, math.Abs(longitude), longChar)
	}

	content := &event.MessageEventContent{
		MsgType:       event.MsgLocation,
		Body:          fmt.Sprintf("Location: %s\n%s\n%s", name, address, url),
		Format:        event.FormatHTML,
		FormattedBody: fmt.Sprintf("Location: <a href='%s'>%s</a><br>%s", url, name, address),
		GeoURI:        fmt.Sprintf("geo:%.5f,%.5f", latitude, longitude),
	}

	return &bridgev2.ConvertedMessagePart{
		Type:    event.EventMessage,
		Content: content,
	}
}

func (mc *MessageConverter) convertForwardMessage(slicePtr *[]*bridgev2.ConvertedMessagePart, ctx context.Context, client *onebot.Client, seg *onebot.ForwardSegment) error {
	//TODO: matrix 使用消息线程（Threading）回复

	data, err := getClient(ctx).DownloadForwardMsg(seg)
	if err != nil {
		return fmt.Errorf("failed to download attachment: %w", err)
	}

	for i, msg := range data {
		var part *bridgev2.ConvertedMessagePart

		mediaParts := make([]*bridgev2.ConvertedMessagePart, 0)

		var contentBuilder strings.Builder

		// 发送者信息
		fmt.Fprintf(&contentBuilder, "[Forward]%s(%s)\n", msg.Sender.Nickname, msg.Sender.UserID)

		// 大概率单个文件
		var hasImage = false
		var isForward = false

		segments := msg.Message.([]onebot.ISegment)
		for _, s := range segments {
			switch v := s.(type) {
			case *onebot.TextSegment:
				fmt.Fprint(&contentBuilder, convertOnebotEmoji(client, v.Content()))
			case *onebot.FaceSegment:
				fmt.Fprint(&contentBuilder, convertOnebotFace(client, v.ID()))
			case *onebot.AtSegment:
				target := v.Target()
				if target == "all" {
					target = "room" // Matrix's mention all
				}
				fmt.Fprintf(&contentBuilder, "@%s", target)
			case *onebot.ImageSegment:
				// mediaParts = append(mediaParts, mc.convertMediaMessage(ctx, v))
				// fmt.Fprint(&contentBuilder, "[Image]")
				hasImage = true
				p := mc.convertMediaMessage(ctx, v)
				mediaParts = append(mediaParts, p)
				fmt.Fprintf(&contentBuilder, "![%s](%s)\n", p.Content.FileName, p.Content.URL)
			case *onebot.MarketFaceSegment:
				// mediaParts = append(mediaParts, mc.convertMediaMessage(ctx, v))
				// fmt.Fprint(&contentBuilder, "[Image]")
				p := mc.convertMediaMessage(ctx, v)
				mediaParts = append(mediaParts, p)
				fmt.Fprintf(&contentBuilder, "![%s](%s)\n", p.Content.FileName, p.Content.URL)
			case *onebot.RecordSegment:

				//BUG: failed to download attachment: failed to download media: &{Segment:{Type:record Data:map[file:<REMOVE>.amr file_size:31028 path:/app/.config/QQ/nt_qq_<REMOVE>/nt_data/Ptt/2026-07/Ori/<REMOVE>.amr url:https://multimedia.nt.qq.com.cn/download?appid=1402&fileid=-<REMOVE>&format=amr&rkey=<REMOVE>]}}
				// pkg/onebot/protocol.go:148

				mediaParts = append(mediaParts, mc.convertMediaMessage(ctx, v))
				fmt.Fprint(&contentBuilder, "[Voice]")
			case *onebot.VideoSegment:
				mediaParts = append(mediaParts, mc.convertMediaMessage(ctx, v))
				fmt.Fprint(&contentBuilder, "[Video]")
			case *onebot.FileSegment:
				mediaParts = append(mediaParts, mc.convertMediaMessage(ctx, v))
				fmt.Fprint(&contentBuilder, "[File]")
			case *onebot.ReplySegment:
				// cm.ReplyTo = &networkid.MessageOptionalPartID{
				// 	MessageID: ids.MakeMessageID(ids.GetPeerID(&msg), v.ID()),
				// }
				//TODO
			case *onebot.ForwardSegment:
				//TODO: 递归实现显示合并消息
				// fmt.Fprint(&contentBuilder, "[Chat History]")

				err := mc.convertForwardMessage(slicePtr, ctx, client, v)
				if err == nil {
					isForward = true
				} else {
					fmt.Fprintf(&contentBuilder, "[Chat History]: %s", err)
				}
			case *onebot.ShareSegment:
				part = mc.convertShareMessage(v.Title(), v.Content(), v.URL())
			case *onebot.JSONSegment:
				part = mc.convertJSONMessage(ctx, v)
			default:
				fmt.Fprintf(&contentBuilder, "[%s]", v.SegmentType())
			}
		}

		if isForward {
			continue
		}

		if part == nil {
			if !hasImage && len(mediaParts) == 1 {
				// 转发者信息
				*slicePtr = append(*slicePtr, &bridgev2.ConvertedMessagePart{
					ID:   networkid.PartID(fmt.Sprintf("%d-0", i)),
					Type: event.EventMessage,
					Content: &event.MessageEventContent{
						MsgType: event.MsgText,
						Body:    fmt.Sprintf("[Forward]%s(%s)\n", msg.Sender.Nickname, msg.Sender.UserID),
					},
				})

				// 文件
				part = mediaParts[0]
				part.ID = networkid.PartID(fmt.Sprintf("%d-1", i))
			} else if len(mediaParts) >= 1 { // mixed image and text
				// mediaParts仅用作判断
				// var imagesMarkdown strings.Builder
				// for _, part := range mediaParts {
				// 	fmt.Fprintf(&imagesMarkdown, "![%s](%s)\n", part.Content.FileName, part.Content.URL)
				// }

				content := contentBuilder.String()
				rendered := format.RenderMarkdown(content, true, false)

				part = &bridgev2.ConvertedMessagePart{
					ID:   networkid.PartID(fmt.Sprintf("%d", i)),
					Type: event.EventMessage,
					Content: &event.MessageEventContent{
						MsgType:       event.MsgText,
						Format:        event.FormatHTML,
						Body:          content,
						FormattedBody: rendered.FormattedBody,
					},
				}
			} else {
				part = &bridgev2.ConvertedMessagePart{
					ID:   networkid.PartID(fmt.Sprintf("%d", i)),
					Type: event.EventMessage,
					Content: &event.MessageEventContent{
						MsgType: event.MsgText,
						Body:    contentBuilder.String(),
					},
				}
			}
		}

		*slicePtr = append(*slicePtr, part)
	}

	return nil

	// FIXME: https://github.com/mautrix/go 暂不支持m.thread
	// 暂时以文字的形式显示
	// [FORWARD]user1:
	// aaa
	// [FORWARD]user2:
	// [IMAGE]
}

func (mc *MessageConverter) convertShareMessage(title, desc, url string) *bridgev2.ConvertedMessagePart {
	body := fmt.Sprintf("%s\n\n%s\n\n%s", title, desc, url)
	rendered := format.RenderMarkdown(
		fmt.Sprintf("**%s**\n%s\n\n[%s](%s)", title, desc, url, url),
		true,
		false,
	)

	return &bridgev2.ConvertedMessagePart{
		Type: event.EventMessage,
		Content: &event.MessageEventContent{
			Body:          body,
			MsgType:       event.MsgText,
			Format:        event.FormatHTML,
			FormattedBody: rendered.FormattedBody,
		},
	}
}

func (mc *MessageConverter) reploadAttachment(ctx context.Context, seg onebot.ISegment) (*bridgev2.ConvertedMessagePart, error) {
	content := &event.MessageEventContent{
		Info: &event.FileInfo{},
	}

	fileName, data, err := getClient(ctx).DownloadMedia(seg)
	if err != nil {
		return nil, fmt.Errorf("failed to download attachment: %w", err)
	}

	mime := mimetype.Detect(data)
	if filepath.Ext(fileName) == "" {
		fileName = fileName + mime.Extension()
	}
	content.Info.Size = len(data)
	content.FileName = fileName

	content.URL, content.File, err = getIntent(ctx).UploadMedia(ctx, getPortal(ctx).MXID, data, fileName, mime.String())
	if err != nil {
		return nil, err
	}

	switch seg.(type) {
	case *onebot.ImageSegment:
		content.MsgType = event.MsgImage
	case *onebot.MarketFaceSegment:
		content.MsgType = event.MsgImage
	case *onebot.VideoSegment:
		content.MsgType = event.MsgVideo
	case *onebot.FileSegment:
		content.MsgType = event.MsgFile
	case *onebot.RecordSegment:
		content.MsgType = event.MsgAudio
		content.MSC3245Voice = &event.MSC3245Voice{}
	}

	//content.Body = fileName
	content.Info.MimeType = mime.String()

	return &bridgev2.ConvertedMessagePart{
		Type:    event.EventMessage,
		Content: content,
	}, nil
}

func (mc *MessageConverter) makeMediaFailure(ctx context.Context, err error) *bridgev2.ConvertedMessagePart {
	zerolog.Ctx(ctx).Err(err).Msg("Failed to reupload Onebot attachment")
	return &bridgev2.ConvertedMessagePart{
		Type: event.EventMessage,
		Content: &event.MessageEventContent{
			MsgType: event.MsgNotice,
			Body:    fmt.Sprintf("Failed to upload Onebot attachment: %s", err),
		},
	}
}

func (mc *MessageConverter) addMentions(ctx context.Context, mentions []string, into *event.MessageEventContent) {
	if len(mentions) == 0 {
		return
	}

	into.EnsureHasHTML()

	for _, id := range mentions {
		if id == "room" {
			into.Mentions.Room = true
			continue
		}

		// TODO: get group nickname
		mxid, displayname, err := mc.getBasicUserInfo(ctx, ids.MakeUserID(id))
		if err != nil {
			zerolog.Ctx(ctx).Err(err).Str("id", id).Msg("Failed to get user info")
			continue
		}
		into.Mentions.UserIDs = append(into.Mentions.UserIDs, mxid)
		mentionText := "@" + id
		into.Body = strings.ReplaceAll(into.Body, mentionText, displayname)
		into.FormattedBody = strings.ReplaceAll(into.FormattedBody, mentionText, fmt.Sprintf(`<a href="%s">%s</a>`, mxid.URI().MatrixToURL(), html.EscapeString(displayname)))
	}
}

func (mc *MessageConverter) getBasicUserInfo(ctx context.Context, user networkid.UserID) (id.UserID, string, error) {
	ghost, err := mc.Bridge.GetGhostByID(ctx, user)
	if err != nil {
		return "", "", fmt.Errorf("failed to get ghost by ID: %w", err)
	}
	login := mc.Bridge.GetCachedUserLoginByID(networkid.UserLoginID(user))
	if login != nil {
		return login.UserMXID, ghost.Name, nil
	}
	return ghost.Intent.GetMXID(), ghost.Name, nil
}
