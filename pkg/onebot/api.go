package onebot

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/duo/matrix-pylon/pkg/util"
	"github.com/mitchellh/mapstructure"
)

func (c *Client) GetLoginInfo() (*UserInfo, error) {
	resp, err := c.request(NewGetLoginInfoRequest())
	if err != nil {
		return nil, err
	}

	var info *UserInfo
	err = mapstructure.WeakDecode(resp, &info)

	return info, err
}

func (c *Client) GetUserInfo(userID string) (*UserInfo, error) {
	resp, err := c.request(NewGetUserInfoRequest(userID))
	if err != nil {
		return nil, err
	}

	var info *UserInfo
	err = mapstructure.WeakDecode(resp, &info)

	return info, err
}

func (c *Client) GetGroupInfo(groupID string) (*GroupInfo, error) {
	resp, err := c.request(NewGetGroupInfoRequest(groupID))
	if err != nil {
		return nil, err
	}

	var info *GroupInfo
	err = mapstructure.WeakDecode(resp, &info)

	return info, err
}

func (c *Client) GetFriendList() ([]*UserInfo, error) {
	resp, err := c.request(NewGetFriendListRequest())
	if err != nil {
		return nil, err
	}

	var friends []*UserInfo
	err = mapstructure.WeakDecode(resp, &friends)

	return friends, err
}

func (c *Client) GetGroupList() ([]*GroupInfo, error) {
	resp, err := c.request(NewGetGroupListRequest())
	if err != nil {
		return nil, err
	}

	var groups []*GroupInfo
	err = mapstructure.WeakDecode(resp, &groups)

	return groups, err
}

func (c *Client) GetGroupMemberList(groupID string) ([]*MemberInfo, error) {
	resp, err := c.request(NewGetGroupMemberListRequest(groupID))
	if err != nil {
		return nil, err
	}

	var members []*MemberInfo
	err = mapstructure.WeakDecode(resp, &members)

	return members, err
}

func (c *Client) GetGroupMemberInfo(groupID string, userID string) (*MemberInfo, error) {
	resp, err := c.request(NewGetGroupMemberInfoRequest(groupID, userID))
	if err != nil {
		return nil, err
	}

	var member *MemberInfo
	err = mapstructure.WeakDecode(resp, &member)

	return member, err
}

func (c *Client) SendPrivateMessage(userID string, segments []ISegment) (*SendMessageResponse, error) {
	resp, err := c.request(NewPrivateMsgRequest(userID, segments))
	if err != nil {
		return nil, err
	}

	var msgResp *SendMessageResponse
	err = mapstructure.WeakDecode(resp, &msgResp)

	return msgResp, err
}

func (c *Client) SendGroupMessage(groupID string, segments []ISegment) (*SendMessageResponse, error) {
	resp, err := c.request(NewGroupMsgRequest(groupID, segments))
	if err != nil {
		return nil, err
	}

	var msgResp *SendMessageResponse
	err = mapstructure.WeakDecode(resp, &msgResp)

	return msgResp, err
}

func (c *Client) DeleteMessage(messageID string) error {
	_, err := c.request(NewDeleteMsgRequest(messageID))

	return err
}

// DownloadForwardMsg 调用 get_forward_msg 接口下载合并转发消息的完整内容
//
// 参数 messageID 是"包含 forward 节点的那条消息"的 message_id，
// 而不是 forward 节点 data.id 字段 —— 用后者 NapCat 会报 retcode 1200（消息不存在）。
//
// 响应有两种形态：
//   - [内容消息...]：直接的转发内部消息（如 bot 自己发送的转发）
//   - [包含消息本身]：单条消息、其 forward 节点 content 已被填充（如收到的转发）
//
// 两种形态都可交给 flattenForward 处理：后者会被继续展开嵌套 forward。
//
// 事件触发时机上 NapCat 的消息历史可能尚未就绪（或请求偶发失败），
// 因此按递增间隔重试几次以提高成功率。
//
// 注意：返回的 Message.Message 保持原始 []any 形式，由调用方按需
// 通过 GenerateSegments 转换，与 unmarshalMessage 的行为保持一致。
func (c *Client) DownloadForwardMsg(messageID string) ([]Message, error) {
	if messageID == "" {
		return nil, fmt.Errorf("message has no message_id")
	}

	var lastErr error
	for i, delay := range []time.Duration{0, time.Second, 3 * time.Second} {
		if delay > 0 {
			c.log.Debug().Str("message_id", messageID).Dur("delay", delay).
				Msgf("Retrying get_forward_msg (attempt %d)", i+1)
			time.Sleep(delay)
		}

		resp, err := c.request(NewGetForwardMsgRequest(messageID))
		if err != nil {
			lastErr = err
			c.log.Warn().Err(err).Str("message_id", messageID).Msg("Failed to call get_forward_msg")
			continue
		}

		var f ForwardInfo
		if err := mapstructure.WeakDecode(resp, &f); err != nil {
			lastErr = err
			c.log.Warn().Err(err).Str("message_id", messageID).Msg("Failed to decode get_forward_msg response")
			continue
		}

		if len(f.Messages) == 0 {
			lastErr = fmt.Errorf("get_forward_msg returned empty messages")
			c.log.Warn().Str("message_id", messageID).Msg("get_forward_msg returned empty messages")
			continue
		}

		c.log.Info().Str("message_id", messageID).Int("count", len(f.Messages)).Msg("Downloaded forward messages via get_forward_msg")
		return f.Messages, nil
	}

	return nil, fmt.Errorf("failed to download forward %s: %w", messageID, lastErr)
}

// DownloadMedia 下载媒体。preferHTTP=true 时（forward 消息）优先走 http 直链：
// get_image/get_file 对转发的媒体文件常 context deadline exceeded，而
// data.url（图片 qpic 直链）或 data.file（本身是 http 直链的文件）可直接下载。
// 直链失败不直接 return，继续走下方 API 路径兜底。
func (c *Client) DownloadMedia(seg ISegment, preferHTTP bool) (string, []byte, error) {
	var request *Request
	var url string

	// 用于日志诊断
	var file string

	switch v := seg.(type) {
	case *ImageSegment:
		request = NewGetImageRequest(v.File())
		url = v.URL()
		file = v.File()
	case *MarketFaceSegment:
		request = NewGetMarketFaceRequest(v.File())
		url = v.URL()
		file = v.File()
	case *VideoSegment:
		request = NewGetFileRequest(v.File())
		url = v.URL()
		file = v.File()
	case *FileSegment:
		request = NewGetFileRequest(v.File())
		file = v.File()
	case *RecordSegment:
		request = NewGetRecordRequest(v.File())
		file = v.File()
	default:
		return "", nil, fmt.Errorf("unsupported media type %+v", v.SegmentType())
	}

	if preferHTTP {
		if strings.HasPrefix(url, "http") {
			if name, data, err := util.Download(url); err == nil {
				return name, data, nil
			}
		} else if strings.HasPrefix(file, "http") {
			if name, data, err := util.Download(file); err == nil {
				return name, data, nil
			}
		}
	}

	if seg.SegmentType() == MarketFace || seg.SegmentType() == Video ||
		(seg.SegmentType() == Image && seg.(*ImageSegment).IsSticker()) {
		if strings.HasPrefix(url, "http") {
			return util.Download(url)
		} else {
			// The video has not been processed yet
			time.Sleep(3 * time.Second)
		}
	}

	resp, err := c.request(request)
	if err == nil {
		var f FileInfo
		if err := mapstructure.WeakDecode(resp, &f); err != nil {
			return "", nil, err
		}

		if f.Base64 != "" {
			if data, err := base64.StdEncoding.DecodeString(f.Base64); err == nil {
				return f.FileName, data, nil
			}
		}
	}

	c.log.Warn().
		Err(err).
		Str("type", string(seg.SegmentType())).
		Str("file", file).
		Msg("failed to download media, trying download from http")

	if strings.HasPrefix(url, "http") {
		return util.Download(url)
	}

	return "", nil, fmt.Errorf("failed to download media: %+v | %w", seg, err)
}
