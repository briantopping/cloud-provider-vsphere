/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package vclib

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"net"
	neturl "net/url"
	"sync"

	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/sts"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"
	klog "k8s.io/klog/v2"
)

const (
	userAgentName = "k8s-cloud-provider-vsphere"
)

// VSphereConnection contains information for connecting to vCenter
type VSphereConnection struct {
	Client              *vim25.Client
	Username            string
	Password            string
	Hostname            string
	Port                string
	CACert              string
	Thumbprint          string
	Insecure            bool
	SessionManagerURL   string
	SessionManagerToken string
	RoundTripperCount   uint
	credentialsLock     sync.Mutex
}

var (
	clientLock sync.Mutex
)

// Connect makes connection to vCenter and sets VSphereConnection.Client.
// If connection.Client is already set, it obtains the existing user session.
// if user session is not valid, connection.Client will be set to the new client.
func (connection *VSphereConnection) Connect(ctx context.Context) error {
	var err error
	clientLock.Lock()
	defer clientLock.Unlock()

	if connection.Client == nil {
		connection.Client, err = connection.NewClient(ctx)
		if err != nil {
			klog.Errorf("Failed to create govmomi client. err: %+v", err)
			return err
		}
		return nil
	}
	m := session.NewManager(connection.Client)
	userSession, err := m.UserSession(ctx)
	if err != nil {
		klog.Errorf("Error while obtaining user session. err: %+v", err)
		return err
	}
	if userSession != nil {
		return nil
	}
	klog.Warning("Creating new client session since the existing session is not valid or not authenticated")

	connection.Client, err = connection.NewClient(ctx)
	if err != nil {
		klog.Errorf("Failed to create govmomi client. err: %+v", err)
		return err
	}
	return nil
}

// Signer returns an sts.Signer for use with SAML token auth if connection is configured for such.
// Returns nil if username/password auth is configured for the connection.
func (connection *VSphereConnection) Signer(ctx context.Context, client *vim25.Client) (*sts.Signer, error) {
	// TODO: Add separate fields for certificate and private-key.
	// For now we can leave the config structs and validation as-is and
	// decide to use LoginByToken if the username value is PEM encoded.
	b, _ := pem.Decode([]byte(connection.Username))
	if b == nil {
		return nil, nil
	}

	cert, err := tls.X509KeyPair([]byte(connection.Username), []byte(connection.Password))
	if err != nil {
		klog.Errorf("Failed to load X509 key pair. err: %+v", err)
		return nil, err
	}

	tokens, err := sts.NewClient(ctx, client)
	if err != nil {
		klog.Errorf("Failed to create STS client. err: %+v", err)
		return nil, err
	}

	req := sts.TokenRequest{
		Certificate: &cert,
		Delegatable: true,
	}

	signer, err := tokens.Issue(ctx, req)
	if err != nil {
		klog.Errorf("Failed to issue SAML token. err: %+v", err)
		return nil, err
	}

	return signer, nil
}

// login calls SessionManager.LoginByToken if certificate and private key are configured,
// otherwise calls SessionManager.Login with user and password.
func (connection *VSphereConnection) login(ctx context.Context, client *vim25.Client) error {
	m := session.NewManager(client)
	connection.credentialsLock.Lock()
	defer connection.credentialsLock.Unlock()

	if connection.SessionManagerURL != "" {
		token, err := GetSharedToken(ctx, SharedTokenOptions{
			URL:   connection.SessionManagerURL,
			Token: connection.SessionManagerToken,
		})
		if err != nil {
			klog.Errorf("error getting shared session token: %s", err)
			return err
		}
		if err := m.CloneSession(ctx, token); err != nil {
			klog.Errorf("error getting shared cloned session token: %s", err)
			return err
		}
		return nil
	}

	signer, err := connection.Signer(ctx, client)
	if err != nil {
		return err
	}

	if signer == nil {
		klog.V(3).Infof("SessionManager.Login with username %q", connection.Username)
		return m.Login(ctx, neturl.UserPassword(connection.Username, connection.Password))
	}

	klog.V(3).Infof("SessionManager.LoginByToken with certificate %q", connection.Username)

	header := soap.Header{Security: signer}

	return m.LoginByToken(client.WithHeader(ctx, header))
}

// Logout calls SessionManager.Logout for the given connection.
func (connection *VSphereConnection) Logout(ctx context.Context) {
	m := session.NewManager(connection.Client)
	if err := m.Logout(ctx); err != nil {
		klog.Errorf("Logout failed: %s", err)
	}
}

// NewClient creates a new govmomi client for the VSphereConnection obj
func (connection *VSphereConnection) NewClient(ctx context.Context) (*vim25.Client, error) {
	url, err := soap.ParseURL(net.JoinHostPort(connection.Hostname, connection.Port))
	if err != nil {
		klog.Errorf("Failed to parse URL: %s. err: %+v", url, err)
		return nil, err
	}

	sc := soap.NewClient(url, connection.Insecure)

	if ca := connection.CACert; ca != "" {
		if err := sc.SetRootCAs(ca); err != nil {
			return nil, err
		}
	}

	tpHost := connection.Hostname + ":" + connection.Port
	sc.SetThumbprint(tpHost, connection.Thumbprint)

	client, err := vim25.NewClient(ctx, sc)
	if err != nil {
		klog.Errorf("Failed to create new client. err: %+v", err)
		return nil, err
	}
	client.UserAgent = userAgentName
	err = connection.login(ctx, client)
	if err != nil {
		return nil, err
	}

	if connection.RoundTripperCount == 0 {
		connection.RoundTripperCount = RoundTripperDefaultCount
	}
	client.RoundTripper = vim25.Retry(client.RoundTripper, vim25.TemporaryNetworkError(int(connection.RoundTripperCount)))
	return client, nil
}

// UpdateCredentials updates username and password.
// Note: Updated username and password will be used when there is no session active
func (connection *VSphereConnection) UpdateCredentials(username string, password string, sessionmgrURL string, sessionmgrToken string) {
	connection.credentialsLock.Lock()
	defer connection.credentialsLock.Unlock()
	connection.Username = username
	connection.Password = password
	connection.SessionManagerURL = sessionmgrURL
	connection.SessionManagerToken = sessionmgrToken
}
