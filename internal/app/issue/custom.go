package issue

import (
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

// Custom is how a custom field on an issue's own screen is edited. It is read
// off the schema editmeta sent — the type, an array's element type and the
// plugin key — and never off a field's id or name, which differ per site.
type Custom uint8

// The shapes of custom field this client edits. CustomNone stays read-only.
const (
	CustomNone Custom = iota
	CustomText
	CustomURL
	CustomDoc
	CustomNumber
	CustomDate
	CustomDateTime
	CustomLabels
	CustomSelect
	CustomMulti
	CustomCascade
	CustomUser
	CustomUsers
)

// Chooses reports a field filled from a list rather than typed.
func (k Custom) Chooses() bool {
	switch k {
	case CustomSelect, CustomMulti, CustomCascade, CustomUser, CustomUsers:
		return true
	default:
		return false
	}
}

// Multiple reports a field that holds more than one value.
func (k Custom) Multiple() bool { return k == CustomMulti || k == CustomUsers }

// People reports a field that holds accounts.
func (k Custom) People() bool { return k == CustomUser || k == CustomUsers }

// choosable are the schema types whose values are a list the screen states.
var choosable = []string{"option", "version", "component", "group"}

// DateTimeLayout is how a date-and-time field is typed and shown, in the
// account's own zone.
const DateTimeLayout = "2006-01-02 15:04"

// CustomOf decides whether a screen field is edited here at all. A field the
// screen does not let be set, or a shape with no editor, is CustomNone.
func CustomOf(meta jira.FieldMeta) Custom {
	s := meta.Field.Schema
	if s.Custom == "" || !slices.Contains(meta.Operations, "set") {
		return CustomNone
	}
	switch {
	case s.Type == "doc", strings.HasSuffix(s.Custom, ":textarea"):
		return CustomDoc
	case strings.HasSuffix(s.Custom, ":url"):
		return CustomURL
	}
	allowed := len(meta.AllowedValues) > 0
	switch s.Type {
	case "string":
		return CustomText
	case "number":
		return CustomNumber
	case "date":
		return CustomDate
	case "datetime":
		return CustomDateTime
	case "user":
		return CustomUser
	case "option-with-child":
		if allowed {
			return CustomCascade
		}
	case "array":
		switch {
		case s.Items == "string":
			return CustomLabels
		case s.Items == "user":
			return CustomUsers
		case slices.Contains(choosable, s.Items) && allowed:
			return CustomMulti
		}
	default:
		if slices.Contains(choosable, s.Type) && allowed {
			return CustomSelect
		}
	}
	return CustomNone
}

// ValueFits reports whether the value the issue carries is in the shape the
// editor writes back. One that is not — a document field the site sent as a
// plain string — is left read-only rather than overwritten from a guess.
func ValueFits(k Custom, v jira.FieldValue, present bool) bool {
	if !present || v.Kind == jira.KindEmpty {
		return true
	}
	switch k {
	case CustomText, CustomURL:
		return v.Kind == jira.KindText
	case CustomDoc:
		return v.Kind == jira.KindDoc
	case CustomNumber:
		return v.Kind == jira.KindNumber
	case CustomDate:
		return v.Kind == jira.KindDate
	case CustomDateTime:
		return v.Kind == jira.KindTime
	case CustomLabels, CustomMulti:
		return v.Kind == jira.KindOptions || v.Kind == jira.KindOption
	case CustomSelect, CustomCascade:
		return v.Kind == jira.KindOption
	case CustomUser:
		return v.Kind == jira.KindUser
	case CustomUsers:
		return v.Kind == jira.KindUsers || v.Kind == jira.KindUser
	default:
		return false
	}
}

// What ParseTyped refuses.
var (
	ErrTypedNumber = errors.New("not a number")
	ErrTypedDate   = errors.New("not a date")
	ErrTypedTime   = errors.New("not a date and time")
	ErrTypedURL    = errors.New("not a whole address")
)

// ParseTyped reads what was typed into a typed custom field as the value it
// writes, in loc when it is a time.
func ParseTyped(k Custom, text string, loc *time.Location) (jira.FieldValue, error) {
	switch k {
	case CustomNumber:
		n, err := strconv.ParseFloat(strings.ReplaceAll(text, ",", "."), 64)
		if err != nil {
			return jira.FieldValue{}, ErrTypedNumber
		}
		return jira.FieldValue{Kind: jira.KindNumber, Number: n}, nil
	case CustomDate:
		d, err := jira.ParseDate(text)
		if err != nil {
			return jira.FieldValue{}, ErrTypedDate
		}
		return jira.FieldValue{Kind: jira.KindDate, Date: d}, nil
	case CustomDateTime:
		if loc == nil {
			loc = time.UTC
		}
		at, err := time.ParseInLocation(DateTimeLayout, text, loc)
		if err != nil {
			if at, err = time.Parse(time.RFC3339, text); err != nil {
				return jira.FieldValue{}, ErrTypedTime
			}
		}
		return jira.FieldValue{Kind: jira.KindTime, Time: at}, nil
	case CustomURL:
		u, err := url.Parse(text)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return jira.FieldValue{}, ErrTypedURL
		}
		return jira.FieldValue{Kind: jira.KindText, Text: text}, nil
	case CustomLabels:
		labels := SplitLabels(text)
		opts := make([]jira.Option, len(labels))
		for i, l := range labels {
			opts[i] = jira.Option{Label: l}
		}
		return jira.FieldValue{Kind: jira.KindOptions, Options: opts}, nil
	default:
		return jira.FieldValue{Kind: jira.KindText, Text: text}, nil
	}
}

// OptionKey identifies a chosen option, its second level included.
func OptionKey(o jira.Option) string {
	if len(o.Children) > 0 {
		return o.ID + "/" + o.Children[0].ID
	}
	return o.ID
}

// SamePicks reports two choices of the same options, in any order when asSet.
func SamePicks(a, b []jira.Option, asSet bool) bool {
	if len(a) != len(b) {
		return false
	}
	ka, kb := make([]string, len(a)), make([]string, len(b))
	for i := range a {
		ka[i], kb[i] = OptionKey(a[i]), OptionKey(b[i])
	}
	if asSet {
		slices.Sort(ka)
		slices.Sort(kb)
	}
	return slices.Equal(ka, kb)
}

// LabelDiff is what turns the labels was into now.
func LabelDiff(was, now []string) (add, remove []string) {
	for _, l := range now {
		if !slices.Contains(was, l) {
			add = append(add, l)
		}
	}
	for _, l := range was {
		if !slices.Contains(now, l) {
			remove = append(remove, l)
		}
	}
	return add, remove
}

// SplitLabels reads the comma-separated form a label list is typed in. Jira
// refuses a label with a space in it, so the separator is unambiguous.
func SplitLabels(s string) []string {
	out := make([]string, 0, strings.Count(s, ",")+1)
	for _, part := range strings.Split(s, ",") {
		label := strings.Join(strings.Fields(part), "-")
		if label != "" && !slices.Contains(out, label) {
			out = append(out, label)
		}
	}
	return out
}
