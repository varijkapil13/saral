package cloud

import "net/http"

// redacted is what a secret prints as, whatever the verb.
const redacted = "[redacted]"

// credentials are the site account and its API token. Jira Cloud authenticates
// an API token as HTTP basic auth, with the account email as the user name.
type credentials struct {
	email string
	token secret
}

// authorize puts the credentials on a request. It is called last, after every
// other header, so that nothing a caller set can replace the Authorization one.
func (c credentials) authorize(req *http.Request) {
	req.SetBasicAuth(c.email, c.token.value())
}

// secret holds the API token behind a closure rather than in a string field.
// fmt reads unexported fields through reflection and cannot call String on
// them, so a string would be printed verbatim by %#v and by any logger walking
// the client; a func value prints as an address under every verb there is.
type secret struct {
	reveal func() string
}

func newSecret(token string) secret {
	if token == "" {
		return secret{}
	}
	return secret{reveal: func() string { return token }}
}

// String is the redacted form, which is the only form anything printing a
// secret ever gets.
func (s secret) String() string {
	if s.reveal == nil {
		return ""
	}
	return redacted
}

func (s secret) value() string {
	if s.reveal == nil {
		return ""
	}
	return s.reveal()
}
