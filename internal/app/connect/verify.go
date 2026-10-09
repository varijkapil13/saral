package connect

import (
	"context"
	"errors"

	"github.com/varijkapil13/saral/pkg/jira"
)

// ErrNotCloud is a site that says it is Jira Data Center or Server.
var ErrNotCloud = errors.New("this site is Jira Data Center or Server, which is not supported yet")

// Account is what verifying a credential reads: who it belongs to and what the
// site is.
type Account interface {
	jira.Identifier
	jira.ServerInfoReader
}

// Verify reads the account a credential belongs to and refuses a site that is
// not Jira Cloud.
func Verify(ctx context.Context, client Account) (jira.User, error) {
	account, err := client.Me(ctx)
	if err != nil {
		return jira.User{}, err
	}
	if err := refuseNonCloud(ctx, client); err != nil {
		return jira.User{}, err
	}
	return account, nil
}

// refuseNonCloud turns away a site that says it is not Jira Cloud. A site that
// will not say is let through: the identity check has already answered on the
// Cloud API, which is better evidence than a probe that failed.
func refuseNonCloud(ctx context.Context, client jira.ServerInfoReader) error {
	info, err := client.ServerInfo(ctx)
	if err == nil && info.DeploymentType != "" && !info.Cloud() {
		return ErrNotCloud
	}
	return nil
}
