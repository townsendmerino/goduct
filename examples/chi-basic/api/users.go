// Package api is the example backend that goduct's analyzer consumes.
// It is intentionally small but exercises:
//   - GET with path param
//   - GET with query params (one optional)
//   - POST with JSON body and validation
//   - PATCH with path param + body
//   - DELETE with path param, no response body
//   - a referenced enum
//   - a nested struct in a response
package api

import (
	"context"

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

// ---- GET /users/:id ----

type GetUserRequest struct {
	ID string `path:"id" validate:"required"`
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
	Limit  int    `query:"limit"  validate:"min=1,max=100"`
	Cursor string `query:"cursor"`
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
	Email string `json:"email" validate:"required,email"`
	Name  string `json:"name"  validate:"required,min=1"`
	Role  string `json:"role"  validate:"required,oneof=admin viewer member"`
}

// CreateUser creates a new user.
//
// goduct:route  POST /users
// goduct:status 201
// goduct:tag    users
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
