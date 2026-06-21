package types

// ForumGroup is one entry of the forum permission-group directory
// (xf_user_group), served by GET /api/v1/forum/groups: a forum-group id paired
// with its display name. The id is the stable key consumers index on (it
// matches the numeric ids the forum's UserGroupsScope add-on hands a client);
// the name is display data that can change at any time. See CONTEXT.md
// ("Forum group") and ADR 0007.
//
// GroupId is a 32-bit id, so it serializes as a JSON number — unlike the
// 64-bit milpac ids (rankId, positionId, …) which serialize as decimal
// strings.
type ForumGroup struct {
	GroupId   uint32 `json:"groupId"`
	GroupName string `json:"groupName"`
}
