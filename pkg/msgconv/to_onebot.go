package msgconv

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/duo/matrix-pylon/pkg/onebot"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func (mc *MessageConverter) ToOnebot(
	ctx context.Context,
	client *onebot.Client,
	evt *event.Event,
	content *event.MessageEventContent,
	portal *bridgev2.Portal,
) ([]onebot.ISegment, error) {
	ctx = context.WithValue(ctx, contextKeyClient, client)
	ctx = context.WithValue(ctx, contextKeyPortal, portal)

	if evt.Type == event.EventSticker {
		content.MsgType = event.MessageType(event.EventSticker.Type)
	}

	segments := []onebot.ISegment{}

	switch content.MsgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
		segments = append(segments, mc.constructTextMessage(ctx, content)...)
	case event.MessageType(event.EventSticker.Type), event.MsgImage, event.MsgVideo, event.MsgAudio, event.MsgFile:
		data, err := mc.Bridge.Bot.DownloadMedia(ctx, content.URL, content.File)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", bridgev2.ErrMediaDownloadFailed, err)
		}
		segments = append(segments, mc.constructMediaMessage(ctx, content, data)...)
	case event.MsgLocation:
		lat, lng, err := parseGeoURI(content.GeoURI)
		if err != nil {
			return nil, err
		}
		segments = append(segments, mc.constructLocationMessage(ctx, content.Body, lat, lng)...)
	default:
		return nil, fmt.Errorf("%w %s", bridgev2.ErrUnsupportedMessageType, content.MsgType)
	}

	return segments, nil
}

func (mc *MessageConverter) constructTextMessage(ctx context.Context, content *event.MessageEventContent) []onebot.ISegment {
	text, mentions := mc.parseText(ctx, content)
	// TODO: elements app not populate content.Mentions
	if content.Mentions != nil && content.Mentions.Room {
		mentions = append(mentions, "room")
	}

	return constructMentionSegments(text, mentions)
}

// constructMentionSegments 按 mention 关键词切分文本，
// 命中的关键词转为 at 段，其余转为 text 段。
func constructMentionSegments(text string, mentions []string) []onebot.ISegment {
	if len(mentions) == 0 {
		return []onebot.ISegment{onebot.NewText(text)}
	}

	keywords := make([]string, len(mentions))
	for i, m := range mentions {
		keywords[i] = "@" + m
	}

	// OID 可能包含正则特殊字符（如 . ( [ \ 等），必须转义后再编译正则：
	// 否则 MustCompile 会 panic 击穿 worker，或 "." 匹配任意字符导致误判。
	// 注意：仅拼正则时转义，用于 slices.Contains 匹配的 keywords 保持原文。
	patterns := make([]string, len(keywords))
	for i, k := range keywords {
		patterns[i] = regexp.QuoteMeta(k)
	}
	pattern := strings.Join(patterns, "|")
	re := regexp.MustCompile("(?:" + pattern + ")")

	parts := re.Split(text, -1)
	matches := re.FindAllString(text, -1)

	var splits []string
	for i := range parts {
		if parts[i] != "" {
			splits = append(splits, parts[i])
		}
		if i < len(matches) {
			splits = append(splits, matches[i])
		}
	}

	segments := []onebot.ISegment{}
	for _, s := range splits {
		if slices.Contains(keywords, s) {
			if s == "@room" {
				segments = append(segments, onebot.NewAt("all"))
			} else {
				segments = append(segments, onebot.NewAt(s[1:]))
			}
		} else {
			segments = append(segments, onebot.NewText(s))
		}
	}

	return segments
}

func (mc *MessageConverter) constructMediaMessage(_ context.Context, content *event.MessageEventContent, data []byte) []onebot.ISegment {
	fileName := content.Body
	if content.FileName != "" {
		fileName = content.FileName
	}

	base64data := fmt.Sprintf("base64://%s", base64.StdEncoding.EncodeToString(data))

	switch content.MsgType {
	case event.MessageType(event.EventSticker.Type), event.MsgImage:
		return []onebot.ISegment{onebot.NewImage(base64data, fileName)}
	case event.MsgVideo:
		return []onebot.ISegment{onebot.NewVideo(base64data, fileName)}
	case event.MsgAudio:
		return []onebot.ISegment{onebot.NewRecord(base64data, fileName)}
	case event.MsgFile:
		return []onebot.ISegment{onebot.NewFile(base64data, fileName)}
	}

	return []onebot.ISegment{}
}

func (mc *MessageConverter) constructLocationMessage(ctx context.Context, name string, lat, lng float64) []onebot.ISegment {
	agentType := getClient(ctx).GetAgentType()
	if agentType == onebot.AgentNapCat || agentType == onebot.AgentLLOneBot {
		locationJson := fmt.Sprintf(`
		{
			"app": "com.tencent.map",
			"desc": "地图",
			"view": "LocationShare",
			"ver": "0.0.0.1",
			"prompt": "[位置]%s",
			"from": 1,
			"meta": {
			  "Location.Search": {
				"id": "12250896297164027526",
				"name": "%s",
				"address": "%s",
				"lat": "%.5f",
				"lng": "%.5f",
				"from": "plusPanel"
			  }
			},
			"config": {
			  "forward": 1,
			  "autosize": 1,
			  "type": "card"
			}
		}
		`, name, name, name, lat, lng)

		return []onebot.ISegment{onebot.NewJSONSegment(locationJson)}
	}

	return []onebot.ISegment{onebot.NewLocation(lat, lng, name, name)}
}

func parseGeoURI(uri string) (lat, lng float64, err error) {
	if !strings.HasPrefix(uri, "geo:") {
		err = fmt.Errorf("uri doesn't have geo: prefix")
		return
	}
	// Remove geo: prefix and anything after ;
	coordinates, _, _ := strings.Cut(strings.TrimPrefix(uri, "geo:"), ";")

	if splitCoordinates := strings.Split(coordinates, ","); len(splitCoordinates) != 2 {
		err = fmt.Errorf("didn't find exactly two numbers separated by a comma")
	} else if lat, err = strconv.ParseFloat(splitCoordinates[0], 64); err != nil {
		err = fmt.Errorf("latitude is not a number: %w", err)
	} else if lng, err = strconv.ParseFloat(splitCoordinates[1], 64); err != nil {
		err = fmt.Errorf("longitude is not a number: %w", err)
	}
	return
}
