// Package auth defines roles and permissions, and hashes passwords and
// session tokens.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
	"slices"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

type Permission string

const (
	// PermRunExport allows downloading Excel exports of run results.
	PermRunExport Permission = "run.export"
	// PermRunExecute allows starting, resuming, and stopping runs and
	// comparing the rows of a table.
	PermRunExecute Permission = "run.execute"
	// PermRunDelete allows deleting runs and their results.
	PermRunDelete Permission = "run.delete"
	// PermProjectWrite allows creating and editing projects.
	PermProjectWrite Permission = "project.write"
	// PermProjectDelete allows deleting projects.
	PermProjectDelete Permission = "project.delete"
	// PermConnectionManage allows creating, editing, testing, and deleting
	// connections.
	PermConnectionManage Permission = "connection.manage"
	// PermConnectionProtected allows running compares that involve a
	// protected connection. It only matters together with PermRunExecute.
	PermConnectionProtected Permission = "connection.protected"
	// PermUserManage allows managing users and their roles.
	PermUserManage Permission = "user.manage"
)

// Every authenticated user may view projects, connections, and run results;
// the permissions below cover everything beyond that.
var rolePermissions = map[string][]Permission{
	RoleViewer: {PermRunExport},
	RoleOperator: {
		PermRunExport, PermRunExecute, PermProjectWrite,
	},
	RoleAdmin: {
		PermRunExport, PermRunExecute, PermProjectWrite,
		PermRunDelete, PermProjectDelete,
		PermConnectionManage, PermConnectionProtected, PermUserManage,
	},
}

// Roles lists the roles from least to most privileged.
var Roles = []string{RoleViewer, RoleOperator, RoleAdmin}

func ValidRole(role string) bool {
	_, ok := rolePermissions[role]
	return ok
}

func Can(role string, p Permission) bool {
	return slices.Contains(rolePermissions[role], p)
}

// Permissions returns the permissions granted to role.
func Permissions(role string) []Permission {
	return slices.Clone(rolePermissions[role])
}

// bcrypt ignores input beyond 72 bytes, so longer passwords are rejected
// instead of being silently truncated.
const (
	MinPasswordLength = 8
	MaxPasswordLength = 72
	bcryptCost        = 12
)

var ErrPasswordLength = errors.New("password must be between 8 and 72 bytes long")

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)

// ValidUsername reports whether name is 3–64 characters of lowercase
// letters, digits, ".", "_", or "-", starting with a letter or digit.
func ValidUsername(name string) bool {
	return usernamePattern.MatchString(name)
}

func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLength || len(password) > MaxPasswordLength {
		return "", ErrPasswordLength
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	return string(hash), err
}

// dummyHash is compared against when a login names an unknown user, so the
// response time does not reveal whether the user exists.
var dummyHash = sync.OnceValue(func() []byte {
	hash, _ := bcrypt.GenerateFromPassword([]byte("dummy password for timing"), bcryptCost)
	return hash
})

// CheckPassword reports whether password matches hash. An empty hash
// never matches but takes as long as a real comparison.
func CheckPassword(hash, password string) bool {
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyHash(), []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// NewToken returns a random session token and the hash stored for it.
func NewToken() (token string, hash []byte, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	return token, HashToken(token), nil
}

// HashToken returns the value under which a session token is stored, so a
// leaked sessions table does not expose usable tokens.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// RandomPassword returns a password suitable for an initial account.
func RandomPassword() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
