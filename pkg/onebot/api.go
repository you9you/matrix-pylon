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
// Onebot 实现（尤其是 NapCat 的 message_sent 回显）中的 forward 段往往只带 id、
// content 为空，或者仅携带最外层节点而嵌套节点的 content 为空，
// 必须通过该接口按 id 下载才能拿到完整（含嵌套）的消息列表。
//
// 事件触发时机上 NapCat 的消息历史可能尚未就绪（或请求偶发失败），
// 因此按递增间隔重试几次以提高成功率。
//
// 注意：返回的 Message.Message 保持原始 []any 形式，由调用方按需
// 通过 GenerateSegments 转换，与 unmarshalMessage 的行为保持一致。
func (c *Client) DownloadForwardMsg(seg *ForwardSegment) ([]Message, error) {
	id := seg.ID()
	if id == "" {
		return nil, fmt.Errorf("forward segment has no id")
	}

	var lastErr error
	for i, delay := range []time.Duration{0, time.Second, 3 * time.Second} {
		if delay > 0 {
			c.log.Debug().Str("forward_id", id).Dur("delay", delay).
				Msgf("Retrying get_forward_msg (attempt %d)", i+1)
			time.Sleep(delay)
		}

		resp, err := c.request(NewGetForwardMsgRequest(id))
		if err != nil {
			lastErr = err
			c.log.Warn().Err(err).Str("forward_id", id).Msg("Failed to call get_forward_msg")
			continue
		}

		var f ForwardInfo
		if err := mapstructure.WeakDecode(resp, &f); err != nil {
			lastErr = err
			c.log.Warn().Err(err).Str("forward_id", id).Msg("Failed to decode get_forward_msg response")
			continue
		}

		if len(f.Messages) == 0 {
			lastErr = fmt.Errorf("get_forward_msg returned empty messages")
			c.log.Warn().Str("forward_id", id).Msg("get_forward_msg returned empty messages")
			continue
		}

		c.log.Info().Str("forward_id", id).Int("count", len(f.Messages)).Msg("Downloaded forward messages via get_forward_msg")
		return f.Messages, nil
	}

	return nil, fmt.Errorf("failed to download forward %s: %w", id, lastErr)
}

func (c *Client) DownloadMedia(seg ISegment) (string, []byte, error) {
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
