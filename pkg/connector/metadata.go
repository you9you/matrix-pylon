package connector

import (
	"go.mau.fi/util/jsontime"
	"maunium.net/go/mautrix/bridgev2/database"
)

type UserLoginMetadata struct {
	Token string `json:"token"`
}

type GhostMetadata struct {
	LastSync jsontime.Unix `json:"last_sync"`
}

type PortalMetadata struct {
	LastSync jsontime.Unix `json:"last_sync"`
}

func (pc *PylonConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		Portal:    func() any { return &PortalMetadata{} },
		Ghost:     func() any { return &GhostMetadata{} },
		Message:   nil,
		Reaction:  nil,
		UserLogin: func() any { return &UserLoginMetadata{} },
	}
}
