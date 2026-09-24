package cloud

import (
	"context"
	"net/http"

	"github.com/varijkapil13/saral/pkg/jira"
)

var _ jira.ServerInfoReader = (*Client)(nil)

const serverInfoPath = "/rest/api/3/serverInfo"

type apiServerInfo struct {
	BaseURL        string `json:"baseUrl"`
	Version        string `json:"version"`
	BuildNumber    int64  `json:"buildNumber"`
	DeploymentType string `json:"deploymentType"`
	ServerTitle    string `json:"serverTitle"`
}

// ServerInfo reports what the site is. The endpoint answers without
// credentials, so a successful read proves the address is a Jira site and
// nothing about the token.
func (c *Client) ServerInfo(ctx context.Context) (jira.ServerInfo, error) {
	var body apiServerInfo
	err := c.doJSON(ctx, request{
		method: http.MethodGet,
		path:   serverInfoPath,
		kind:   "the site's server information",
		id:     serverInfoPath,
	}, &body)
	if err != nil {
		return jira.ServerInfo{}, err
	}
	return jira.ServerInfo{
		BaseURL:        body.BaseURL,
		Version:        body.Version,
		BuildNumber:    body.BuildNumber,
		DeploymentType: jira.DeploymentType(body.DeploymentType),
		ServerTitle:    body.ServerTitle,
	}, nil
}
