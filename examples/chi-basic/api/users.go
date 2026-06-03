// Package api is the example backend that goduct's analyzer consumes.
// It is intentionally small but exercises:
//   - GET with path + query params (combined argument object on the
//     TS client; ADR 0040 §3 coverage)
//   - GET with int/string/bool/float query params (every adapter
//     query-parse path)
//   - POST with JSON body, validation (required/email/url/len/oneof),
//     a per-status errorresponse, and a goduct:requestexample
//   - PATCH with path param + body
//   - DELETE with path param, no response body
//   - a referenced enum
//   - a nested struct in a response
//   - the v0.4 goduct:example directive on a response
package api

import (
	"context"
	"mime/multipart"

	goduct "github.com/townsendmerino/goduct/runtime"
)

// UserStatus represents whether a user is active, invited, or suspended.
type UserStatus string

const (
	UserStatusActive    UserStatus = "active"
	UserStatusInvited   UserStatus = "invited"
	UserStatusSuspended UserStatus = "suspended"
)

// User is the canonical user shape returned by the API.
type User struct {
	ID      string     `json:"id"`
	Email   string     `json:"email"`
	Name    string     `json:"name"`
	Status  UserStatus `json:"status"`
	Profile *Profile   `json:"profile,omitempty"`
}

// Profile is optional metadata attached to a user.
type Profile struct {
	Bio       string   `json:"bio"`
	AvatarURL string   `json:"avatarUrl,omitempty"`
	Tags      []string `json:"tags"`
}

// ValidationError describes one field-level validation failure on a
// request body. CreateUser returns 400 with this shape per
// goduct:errorresponse 400 ValidationError (ADR 0039 / 0040 §3).
type ValidationError struct {
	Field  string   `json:"field"`
	Errors []string `json:"errors"`
}

// ---- GET /users/:id?include=... ----

type GetUserRequest struct {
	ID      string `path:"id"      validate:"required"`
	Include string `query:"include"` // optional: "profile" | "permissions"
}

// GetUser returns a single user by ID.
//
// goduct:route   GET /users/:id
// goduct:tag     users
// goduct:example {"id":"u_1","email":"alice@example.com","name":"Alice","status":"active"}
func GetUser(ctx context.Context, req GetUserRequest) (*User, error) {
	if req.ID == "" {
		return nil, goduct.BadRequest("id is required")
	}
	// pretend to look up the user
	return &User{ID: req.ID, Email: "u@example.com", Name: "Example", Status: UserStatusActive}, nil
}

// ---- GET /users ----

type ListUsersRequest struct {
	Limit    int      `query:"limit"    validate:"min=1,max=100"`
	Cursor   string   `query:"cursor"`
	Active   *bool    `query:"active"`   // optional: filter by Status == active
	MinScore *float64 `query:"minScore"` // optional: filter by min score
}

type ListUsersResponse struct {
	Users      []User `json:"users"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// ListUsers returns a page of users.
//
// goduct:route GET /users
// goduct:tag   users
func ListUsers(ctx context.Context, req ListUsersRequest) (*ListUsersResponse, error) {
	return &ListUsersResponse{Users: []User{}}, nil
}

// ---- POST /users ----

type CreateUserRequest struct {
	Email        string `json:"email"                  validate:"required,email"`
	Name         string `json:"name"                   validate:"required,min=1"`
	Role         string `json:"role"                   validate:"required,oneof=admin viewer member"`
	Website      string `json:"website,omitempty"      validate:"omitempty,url"`
	ReferralCode string `json:"referralCode,omitempty" validate:"omitempty,len=8"`
}

// CreateUser creates a new user.
//
// goduct:route          POST /users
// goduct:status         201
// goduct:tag            users
// goduct:errorresponse  400 ValidationError
// goduct:requestexample {"email":"alice@example.com","name":"Alice","role":"member"}
func CreateUser(ctx context.Context, req CreateUserRequest) (*User, error) {
	return &User{ID: "u_new", Email: req.Email, Name: req.Name, Status: UserStatusInvited}, nil
}

// ---- PATCH /users/:id ----

type UpdateUserRequest struct {
	ID     string      `path:"id"            validate:"required"`
	Name   *string     `json:"name,omitempty"`
	Status *UserStatus `json:"status,omitempty"`
}

// UpdateUser updates fields on an existing user. Omitted fields are not changed.
//
// goduct:route PATCH /users/:id
// goduct:tag   users
func UpdateUser(ctx context.Context, req UpdateUserRequest) (*User, error) {
	return &User{ID: req.ID, Email: "u@example.com", Name: "Updated", Status: UserStatusActive}, nil
}

// ---- DELETE /users/:id ----

type DeleteUserRequest struct {
	ID string `path:"id" validate:"required"`
}

// DeleteUser removes a user.
//
// goduct:route  DELETE /users/:id
// goduct:status 204
// goduct:tag    users
func DeleteUser(ctx context.Context, req DeleteUserRequest) error {
	_ = req
	return nil
}

// ---- POST /users/:id/avatar (multipart upload, ADR 0042) ----

type UploadAvatarRequest struct {
	UserID  string                `path:"id"             validate:"required"`
	File    *multipart.FileHeader `multipart:"file"      validate:"required"`
	Caption string                `form:"caption"`
}

// UploadAvatar stores a new avatar image for the user.
//
// goduct:route POST /users/:id/avatar
// goduct:tag   users
func UploadAvatar(ctx context.Context, req UploadAvatarRequest) (*User, error) {
	_ = req.File
	return &User{ID: req.UserID, Email: "u@example.com", Name: "Example", Status: UserStatusActive}, nil
}
