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

func (c *Client) DownloadForwardMsg(seg *ForwardSegment) ([]Message, error) {
	request := NewGetForwardMsgRequest(seg.ID())

	resp, err := c.request(request)

	if err == nil {
		var f ForwardInfo
		if err := mapstructure.WeakDecode(resp, &f); err != nil {
			return nil, err
		}

		// 需要将 []interface{} 转换为 []ISegment
		for i, msg := range f.Messages {
			if msgs, ok := msg.Message.([]any); ok {
				f.Messages[i].Message = generateSegments(msgs)
			}
		}

		return f.Messages, nil
	}

	c.log.Error().
		Err(err).
		Str("seg", fmt.Sprintf("%+v", seg)).
		Msg("failed to download forward")

	return nil, fmt.Errorf("failed to download forward: %+v", seg)
}

func (c *Client) DownloadMedia(seg ISegment) (string, []byte, error) {
	var request *Request
	var url string

	switch v := seg.(type) {
	case *ImageSegment:
		request = NewGetImageRequest(v.File())
		url = v.URL()
	case *MarketFaceSegment:
		request = NewGetMarketFaceRequest(v.File())
		url = v.URL()
	case *VideoSegment:
		request = NewGetFileRequest(v.File())
		url = v.URL()
	case *FileSegment:
		request = NewGetFileRequest(v.File())
	case *RecordSegment:
		request = NewGetRecordRequest(v.File())
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

	if resp, err := c.request(request); err == nil {
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

	if strings.HasPrefix(url, "http") {
		return util.Download(url)
	}

	return "", nil, fmt.Errorf("failed to download media: %+v", seg)
}
