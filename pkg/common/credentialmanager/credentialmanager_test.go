/*
Copyright 2016 The Kubernetes Authors.

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

package credentialmanager

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
)

func TestSecretCredentialManagerK8s_GetCredential(t *testing.T) {
	var (
		userKey                 = "username"
		passwordKey             = "password"
		vcSessionURL            = "vc-session-manager-url"
		vcSessionToken          = "vc-session-manager-token"
		testUser                = "user"
		testPassword            = "password"
		testServer              = "0.0.0.0"
		testServer2             = "0.0.1.1"
		testIPv6Server          = "fd01::1"
		testUserServer2         = "user1"
		testPasswordServer2     = "password1"
		testIncorrectServer     = "1.1.1.1"
		testSessionManagerURL   = "https://somemanager.tld/session"
		testSessionManagerToken = "token"
	)
	var (
		secretName      = "vsconf"
		secretNamespace = "kube-system"
	)
	var (
		addSecretOp      = "ADD_SECRET_OP"
		getCredentialsOp = "GET_CREDENTIAL_OP"
		deleteSecretOp   = "DELETE_SECRET_OP"
	)
	type GetCredentialsTest struct {
		server         string
		username       string
		password       string
		vcSessionURL   string
		vcSessionToken string
		err            error
	}
	type OpSecretTest struct {
		secret *corev1.Secret
	}
	type testEnv struct {
		testName       string
		ops            []string
		expectedValues []interface{}
	}

	client := &fake.Clientset{}
	metaObj := metav1.ObjectMeta{
		Name:      secretName,
		Namespace: secretNamespace,
	}

	defaultSecret := &corev1.Secret{
		ObjectMeta: metaObj,
		Data: map[string][]byte{
			testServer + "." + userKey:     []byte(testUser),
			testServer + "." + passwordKey: []byte(testPassword),
		},
	}

	multiVCSecret := &corev1.Secret{
		ObjectMeta: metaObj,
		Data: map[string][]byte{
			testServer + "." + userKey:      []byte(testUser),
			testServer + "." + passwordKey:  []byte(testPassword),
			testServer2 + "." + userKey:     []byte(testUserServer2),
			testServer2 + "." + passwordKey: []byte(testPasswordServer2),
		},
	}

	multiVCSecretMixedWithSessionManager := &corev1.Secret{
		ObjectMeta: metaObj,
		Data: map[string][]byte{
			testServer + "." + userKey:         []byte(testUser),
			testServer + "." + passwordKey:     []byte(testPassword),
			testServer2 + "." + vcSessionURL:   []byte(testSessionManagerURL),
			testServer2 + "." + vcSessionToken: []byte(testSessionManagerToken),
		},
	}

	ipv6CompatSecret := &corev1.Secret{
		ObjectMeta: metaObj,
		Data: map[string][]byte{
			userKey + "_0":     []byte(testUser),
			passwordKey + "_0": []byte(testPassword),
			"server_0":         []byte(testServer),
			userKey + "_1":     []byte(testUserServer2),
			passwordKey + "_1": []byte(testPasswordServer2),
			"server_1":         []byte(testIPv6Server),
		},
	}

	emptySecret := &corev1.Secret{
		ObjectMeta: metaObj,
		Data:       map[string][]byte{},
	}

	tests := []testEnv{
		{
			testName: "Deleting secret should give the credentials from cache",
			ops:      []string{addSecretOp, getCredentialsOp, deleteSecretOp, getCredentialsOp},
			expectedValues: []interface{}{
				OpSecretTest{
					secret: defaultSecret,
				},
				GetCredentialsTest{
					username: testUser,
					password: testPassword,
					server:   testServer,
				},
				OpSecretTest{
					secret: defaultSecret,
				},
				GetCredentialsTest{
					username: testUser,
					password: testPassword,
					server:   testServer,
				},
			},
		},
		{
			testName: "Add secret and get credentials",
			ops:      []string{addSecretOp, getCredentialsOp},
			expectedValues: []interface{}{
				OpSecretTest{
					secret: defaultSecret,
				},
				GetCredentialsTest{
					username: testUser,
					password: testPassword,
					server:   testServer,
				},
			},
		},
		{
			testName: "Getcredentials should fail by not adding at secret at first time",
			ops:      []string{getCredentialsOp},
			expectedValues: []interface{}{
				GetCredentialsTest{
					username: testUser,
					password: testPassword,
					server:   testServer,
					err:      ErrCredentialsNotFound,
				},
			},
		},
		{
			testName: "GetCredential should fail to get credentials from empty secrets",
			ops:      []string{addSecretOp, getCredentialsOp},
			expectedValues: []interface{}{
				OpSecretTest{
					secret: emptySecret,
				},
				GetCredentialsTest{
					server: testServer,
					err:    ErrCredentialMissing,
				},
			},
		},
		{
			testName: "GetCredential should fail to get credentials for invalid server",
			ops:      []string{addSecretOp, getCredentialsOp},
			expectedValues: []interface{}{
				OpSecretTest{
					secret: defaultSecret,
				},
				GetCredentialsTest{
					server: testIncorrectServer,
					err:    ErrCredentialsNotFound,
				},
			},
		},
		{
			testName: "GetCredential for multi-vc",
			ops:      []string{addSecretOp, getCredentialsOp},
			expectedValues: []interface{}{
				OpSecretTest{
					secret: multiVCSecret,
				},
				GetCredentialsTest{
					server:   testServer2,
					username: testUserServer2,
					password: testPasswordServer2,
				},
			},
		},
		{
			testName: "GetCredential for multi-vc with session manager",
			ops:      []string{addSecretOp, getCredentialsOp},
			expectedValues: []interface{}{
				OpSecretTest{
					secret: multiVCSecretMixedWithSessionManager,
				},
				GetCredentialsTest{
					server:         testServer2,
					vcSessionURL:   testSessionManagerURL,
					vcSessionToken: testSessionManagerToken,
				},
			},
		},
		{
			testName: "GetCredential for alternative IPv6 server address compatable format",
			ops:      []string{addSecretOp, getCredentialsOp},
			expectedValues: []interface{}{
				OpSecretTest{
					secret: ipv6CompatSecret,
				},
				GetCredentialsTest{
					server:   testServer,
					username: testUser,
					password: testPassword,
				},
				GetCredentialsTest{
					server:   testIPv6Server,
					username: testUserServer2,
					password: testPasswordServer2,
				},
			},
		},
	}

	informerFactory := informers.NewSharedInformerFactory(client, 0)
	secretInformer := informerFactory.Core().V1().Secrets()
	secretCredentialManager := &CredentialManager{
		SecretName:      secretName,
		SecretNamespace: secretNamespace,
		SecretLister:    secretInformer.Lister(),
		Cache: &SecretCache{
			VirtualCenter: make(map[string]*Credential),
		},
	}
	cleanupSecretCredentialManager := func() {
		secretCredentialManager.Cache.Secret = nil
		for key := range secretCredentialManager.Cache.VirtualCenter {
			delete(secretCredentialManager.Cache.VirtualCenter, key)
		}
		secrets, err := secretCredentialManager.SecretLister.List(labels.Everything())
		if err != nil {
			t.Fatal("Failed to get all secrets from sharedInformer. error: ", err)
		}
		for _, secret := range secrets {
			secretInformer.Informer().GetIndexer().Delete(secret)
		}
	}

	for _, test := range tests {
		t.Logf("Executing Testcase: %s", test.testName)
		for ntest, op := range test.ops {
			switch op {
			case addSecretOp:
				expected := test.expectedValues[ntest].(OpSecretTest)
				t.Logf("Adding secret: %s", expected.secret)
				err := secretInformer.Informer().GetIndexer().Add(expected.secret)
				if err != nil {
					t.Fatalf("Failed to add secret to internal cache: %v", err)
				}
			case getCredentialsOp:
				expected := test.expectedValues[ntest].(GetCredentialsTest)
				credential, err := secretCredentialManager.GetCredential(expected.server)
				t.Logf("Retrieving credentials for server %s", expected.server)
				if err != expected.err {
					t.Fatalf("Fail to get credentials with error: %v", err)
				}
				if expected.err == nil {
					if expected.username != credential.User ||
						expected.password != credential.Password ||
						expected.vcSessionToken != credential.VCSessionManagerToken ||
						expected.vcSessionURL != credential.VCSessionManagerURL {
						t.Fatalf("Received credentials %v "+
							"are different than actual credential user:%s password:%s", credential, expected.username,
							expected.password)
					}
				}
			case deleteSecretOp:
				expected := test.expectedValues[ntest].(OpSecretTest)
				t.Logf("Deleting secret: %s", expected.secret)
				err := secretInformer.Informer().GetIndexer().Delete(expected.secret)
				if err != nil {
					t.Fatalf("Failed to delete secret to internal cache: %v", err)
				}
			}
		}
		cleanupSecretCredentialManager()
	}
}

func TestParseSecretConfig(t *testing.T) {
	var (
		testUsername = "Admin"
		testPassword = "Password"
		testIP       = "10.20.30.40"
		testIPv6     = "fd01::1"
	)
	var testcases = []struct {
		testName      string
		data          map[string][]byte
		config        map[string]*Credential
		expectedError error
	}{
		{
			testName: "Valid username and password",
			data: map[string][]byte{
				"10.20.30.40.username": []byte(testUsername),
				"10.20.30.40.password": []byte(testPassword),
			},
			config: map[string]*Credential{
				testIP: {
					User:     testUsername,
					Password: testPassword,
				},
			},
			expectedError: nil,
		},
		{
			testName: "Valid username and password with suffix '\\n'(Load from files in SecretDirectory)",
			data: map[string][]byte{
				"10.20.30.40.username": []byte(testUsername + "\n"),
				"10.20.30.40.password": []byte(testPassword + "\n"),
			},
			config: map[string]*Credential{
				testIP: {
					User:     testUsername,
					Password: testPassword,
				},
			},
			expectedError: nil,
		},
		{
			testName: "Invalid username key with valid password key",
			data: map[string][]byte{
				"10.20.30.40.usernam":  []byte(testUsername),
				"10.20.30.40.password": []byte(testPassword),
			},
			config:        nil,
			expectedError: ErrUnknownSecretKey,
		},
		{
			testName: "Missing username",
			data: map[string][]byte{
				"10.20.30.40.password": []byte(testPassword),
			},
			config: map[string]*Credential{
				testIP: {
					Password: testPassword,
				},
			},
			expectedError: ErrCredentialMissing,
		},
		{
			testName: "Missing password",
			data: map[string][]byte{
				"10.20.30.40.username": []byte(testUsername),
			},
			config: map[string]*Credential{
				testIP: {
					User: testUsername,
				},
			},
			expectedError: ErrCredentialMissing,
		},
		{
			testName: "Missing session manager token",
			data: map[string][]byte{
				"10.20.30.40.vc-session-manager-url": []byte("https://something.tld/session"),
			},
			config: map[string]*Credential{
				testIP: {
					VCSessionManagerURL: "https://something.tld/session",
				},
			},
			expectedError: nil,
		},
		{
			testName: "Missing session manager url",
			data: map[string][]byte{
				"10.20.30.40.vc-session-manager-token": []byte("token"),
			},
			config: map[string]*Credential{
				testIP: {
					VCSessionManagerToken: "token",
				},
			},
			expectedError: ErrCredentialMissing,
		},
		{
			testName: "Valid session manager configuration",
			data: map[string][]byte{
				"10.20.30.40.vc-session-manager-url":   []byte("https://something.tld/session"),
				"10.20.30.40.vc-session-manager-token": []byte("token"),
			},
			config: map[string]*Credential{
				testIP: {
					VCSessionManagerURL:   "https://something.tld/session",
					VCSessionManagerToken: "token",
				},
			},
		},
		{
			testName: "IP with unknown key",
			data: map[string][]byte{
				"10.20.30.40": []byte(testUsername),
			},
			config:        nil,
			expectedError: ErrUnknownSecretKey,
		},
		{
			testName: "Alternative IPv6 compatible secret",
			data: map[string][]byte{
				"username_0":   []byte(testUsername),
				"password_0":   []byte(testPassword),
				"server_0":     []byte(testIPv6),
				"username_foo": []byte(testUsername + "foo"),
				"password_foo": []byte(testPassword + "foo"),
				"server_foo":   []byte(testIP),
			},
			config: map[string]*Credential{
				testIPv6: {
					User:     testUsername,
					Password: testPassword,
				},
				testIP: {
					User:     testUsername + "foo",
					Password: testPassword + "foo",
				},
			},
		},
		{
			testName: "Alternative IPv6 compatible secret: missing password",
			data: map[string][]byte{
				"username_0": []byte(testUsername),
				"server_0":   []byte(testIPv6),
			},
			expectedError: ErrCredentialMissing,
		},
		{
			testName: "Alternative IPv6 compatible secret: missing username",
			data: map[string][]byte{
				"password_0": []byte(testPassword),
				"server_0":   []byte(testIPv6),
			},
			expectedError: ErrCredentialMissing,
		},
		{
			testName: "Alternative IPv6 compatible secret: password with no matching server",
			data: map[string][]byte{
				"username_0": []byte(testUsername),
				"password_0": []byte(testPassword),
				"server_0":   []byte(testIPv6),
				"password_1": []byte(testPassword),
			},
			expectedError: ErrIncompleteCredentialSet,
		},
		{
			testName: "Alternative IPv6 compatible secret: username with no matching server",
			data: map[string][]byte{
				"username_0": []byte(testUsername),
				"password_0": []byte(testPassword),
				"server_0":   []byte(testIPv6),
				"username_1": []byte(testPassword),
			},
			expectedError: ErrIncompleteCredentialSet,
		},
		{
			testName: "Alternative IPv6 compatible secret: missing suffixes",
			data: map[string][]byte{
				"username_": []byte(testUsername),
				"password_": []byte(testPassword),
				"server_":   []byte(testIPv6),
			},
			expectedError: ErrUnknownSecretKey,
		},
		{
			testName: "Mixing legacy and Alternative IPv6 compatible secret",
			data: map[string][]byte{
				"10.20.30.40.username": []byte(testUsername),
				"10.20.30.40.password": []byte(testPassword),
				"username_0":           []byte(testUsername + "alt"),
				"password_0":           []byte(testPassword + "alt"),
				"server_0":             []byte(testIPv6),
			},
			config: map[string]*Credential{
				testIP: {
					User:     testUsername,
					Password: testPassword,
				},
				testIPv6: {
					User:     testUsername + "alt",
					Password: testPassword + "alt",
				},
			},
			expectedError: nil,
		},
		{
			testName: "Alternative IPv6 compatible secret: server key missing suffix",
			data: map[string][]byte{
				"username_": []byte(testUsername),
			},
			expectedError: ErrUnknownSecretKey,
		},
		{
			testName: "Alternative IPv6 compatible secret: username missing suffix",
			data: map[string][]byte{
				"username_": []byte(testUsername),
			},
			expectedError: ErrUnknownSecretKey,
		},
		{
			testName: "Alternative IPv6 compatible secret: password missing suffix",
			data: map[string][]byte{
				"password_": []byte(testPassword),
			},
			expectedError: ErrUnknownSecretKey,
		},
	}

	resultConfig := make(map[string]*Credential)
	cleanupResultConfig := func(config map[string]*Credential) {
		for k := range config {
			delete(config, k)
		}
	}

	for _, testcase := range testcases {
		err := parseConfig(testcase.data, resultConfig)
		t.Logf("Executing Testcase: %s", testcase.testName)
		if err != testcase.expectedError {
			t.Fatalf("Parsing Secret failed for data %+v: %s", testcase.data, err)
		}
		if testcase.config != nil && !reflect.DeepEqual(testcase.config, resultConfig) {
			t.Fatalf("Parsing Secret failed for data %+v expected config %+v and actual config %+v",
				testcase.data, resultConfig, testcase.config)
		}
		cleanupResultConfig(resultConfig)
	}
}
