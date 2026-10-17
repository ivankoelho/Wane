package models

import (
	"bytes"
	"time"

	"github.com/google/uuid"
)

// Internal chat between users of the same organization (docs/superpowers/specs/
// 2026-10-16-chat-interno-nativo-design.md). It is a domain of its own: the
// author is a users row, the organization is the tenant, a team channel's
// members are the team's team_members. Nothing here duplicates conversation or
// contact data; context_* only references it.

// ChatChannelKind says how a channel's membership is decided.
type ChatChannelKind string

const (
	// ChatChannelKindTeam: one per team; membership IS team_members (no rows in
	// chat_channel_members).
	ChatChannelKindTeam ChatChannelKind = "team"
	// ChatChannelKindChannel: free channel, private; membership in chat_channel_members.
	ChatChannelKindChannel ChatChannelKind = "channel"
	// ChatChannelKindDM: exactly two users, identified by DMUserLowID/DMUserHighID.
	ChatChannelKindDM ChatChannelKind = "dm"
)

// ChatChannel is a team channel, a free channel or a direct message.
//
// Uniqueness lives in partial unique indexes (see database coreIndexes): one
// team channel per (organization, team) and one DM per (organization, pair).
type ChatChannel struct {
	BaseModel
	OrganizationID uuid.UUID       `gorm:"type:uuid;index;not null" json:"organization_id"`
	Kind           ChatChannelKind `gorm:"size:20;not null" json:"kind"`
	TeamID         *uuid.UUID      `gorm:"type:uuid;index" json:"team_id,omitempty"`
	Name           string          `gorm:"size:100" json:"name"`
	CreatedByID    *uuid.UUID      `gorm:"type:uuid" json:"created_by_id,omitempty"`

	// Canonical DM pair: low < high (see CanonicalDMPair). Only set for kind=dm.
	DMUserLowID  *uuid.UUID `gorm:"column:dm_user_low_id;type:uuid" json:"-"`
	DMUserHighID *uuid.UUID `gorm:"column:dm_user_high_id;type:uuid" json:"-"`
}

func (ChatChannel) TableName() string { return "chat_channels" }

// ChatChannelMember is membership of a free channel or a DM. Team channels
// never have rows here. Hard-deleted on removal (no soft delete), so the unique
// (channel_id, user_id) never blocks a later re-add.
type ChatChannelMember struct {
	ID             uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	OrganizationID uuid.UUID `gorm:"type:uuid;index;not null" json:"organization_id"`
	ChannelID      uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_chat_member_channel_user" json:"channel_id"`
	UserID         uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_chat_member_channel_user;index" json:"user_id"`
	Role           string    `gorm:"size:20;not null;default:'member'" json:"role"` // owner, member
	JoinedAt       time.Time `gorm:"autoCreateTime" json:"joined_at"`
}

func (ChatChannelMember) TableName() string { return "chat_channel_members" }

// ChatMessage is a message in a channel. ParentID is reserved for threads;
// ContextType/ContextID only reference a Whatc entity (never copy it).
type ChatMessage struct {
	BaseModel
	OrganizationID uuid.UUID  `gorm:"type:uuid;index;not null" json:"organization_id"`
	ChannelID      uuid.UUID  `gorm:"type:uuid;not null" json:"channel_id"`
	UserID         uuid.UUID  `gorm:"type:uuid;index;not null" json:"user_id"`
	Body           string     `gorm:"type:text;not null" json:"body"`
	ParentID       *uuid.UUID `gorm:"type:uuid;index" json:"parent_id,omitempty"`
	ContextType    string     `gorm:"size:30" json:"context_type,omitempty"`
	ContextID      *uuid.UUID `gorm:"type:uuid" json:"context_id,omitempty"`
	EditedAt       *time.Time `json:"edited_at,omitempty"`
}

func (ChatMessage) TableName() string { return "chat_messages" }

// ChatRead is one user's read marker in one channel. It is separate from
// membership because team-channel members have no membership row. The marker is
// the (created_at, id) of the last read message, the same cursor shape the
// WhatsApp message list uses.
type ChatRead struct {
	ID                uuid.UUID  `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	OrganizationID    uuid.UUID  `gorm:"type:uuid;index;not null" json:"organization_id"`
	ChannelID         uuid.UUID  `gorm:"type:uuid;not null;uniqueIndex:idx_chat_read_channel_user" json:"channel_id"`
	UserID            uuid.UUID  `gorm:"type:uuid;not null;uniqueIndex:idx_chat_read_channel_user" json:"user_id"`
	LastReadAt        time.Time  `gorm:"not null" json:"last_read_at"`
	LastReadMessageID *uuid.UUID `gorm:"type:uuid" json:"last_read_message_id,omitempty"`
}

func (ChatRead) TableName() string { return "chat_reads" }

// CanonicalDMPair orders two user IDs (byte-wise) so a DM between A and B has
// one identity regardless of who opens it. The unique index on the pair then
// makes a concurrent double-create impossible.
func CanonicalDMPair(a, b uuid.UUID) (low, high uuid.UUID) {
	if bytes.Compare(a[:], b[:]) <= 0 {
		return a, b
	}
	return b, a
}
