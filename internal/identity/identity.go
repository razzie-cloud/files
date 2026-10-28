package identity

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

type User struct{ ID, Name string }

func FromRequest(r *http.Request) (User, error) {
	values := r.Header.Values("X-Auth-Request-User")
	if len(values) != 1 {
		return User{}, errors.New("verified user required")
	}
	id, e := uuid.Parse(values[0])
	if e != nil || id == uuid.Nil || id.String() != values[0] {
		return User{}, errors.New("invalid user identity")
	}
	name := r.Header.Get("X-Auth-Request-Name")
	if len(r.Header.Values("X-Auth-Request-Name")) > 1 || strings.ContainsAny(name, "\r\n") {
		return User{}, errors.New("invalid display name")
	}
	return User{ID: id.String(), Name: name}, nil
}
