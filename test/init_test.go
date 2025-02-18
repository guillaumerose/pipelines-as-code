//go:build e2e
// +build e2e

package test

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	ghlib "github.com/google/go-github/v39/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	defaultTimeout   = 10 * time.Minute
	mainBranch       = "main"
	pullRequestEvent = "pull_request"
)

type E2EOptions struct {
	Repo, Organization string
	DirectWebhook      bool
}

func ghtearDown(ctx context.Context, t *testing.T, runcnx *params.Run, ghprovider github.Provider,
	prNumber int, ref string, targetNS string, opts E2EOptions) {
	runcnx.Clients.Log.Infof("Closing PR %d", prNumber)
	if prNumber != -1 {
		state := "closed"
		_, _, err := ghprovider.Client.PullRequests.Edit(ctx,
			opts.Organization, opts.Repo, prNumber,
			&ghlib.PullRequest{State: &state})
		if err != nil {
			t.Fatal(err)
		}
	}
	nsTearDown(ctx, t, runcnx, targetNS)
	runcnx.Clients.Log.Infof("Deleting Ref %s", ref)
	_, err := ghprovider.Client.Git.DeleteRef(ctx, opts.Organization, opts.Repo, ref)
	assert.NilError(t, err)
}

func nsTearDown(ctx context.Context, t *testing.T, runcnx *params.Run, targetNS string) {
	runcnx.Clients.Log.Infof("Deleting NS %s", targetNS)
	err := runcnx.Clients.Kube.CoreV1().Namespaces().Delete(ctx, targetNS, metav1.DeleteOptions{})
	assert.NilError(t, err)
}

func bitbucketCloudSetup(ctx context.Context) (*params.Run, E2EOptions, bitbucketcloud.Provider, error) {
	bitbucketCloudUser := os.Getenv("TEST_BITBUCKET_CLOUD_USER")
	bitbucketCloudToken := os.Getenv("TEST_BITBUCKET_CLOUD_TOKEN")
	bitbucketWSOwner := os.Getenv("TEST_BITBUCKET_CLOUD_E2E_REPOSITORY")
	bitbucketCloudAPIURL := os.Getenv("TEST_BITBUCKET_CLOUD_API_URL")

	for _, value := range []string{
		"BITBUCKET_CLOUD_TOKEN", "BITBUCKET_CLOUD_E2E_REPOSITORY", "BITBUCKET_CLOUD_API_URL",
	} {
		if env := os.Getenv("TEST_" + value); env == "" {
			return nil, E2EOptions{}, bitbucketcloud.Provider{}, fmt.Errorf("\"TEST_%s\" env variable is required, skipping", value)
		}
	}

	splitted := strings.Split(bitbucketWSOwner, "/")

	run := &params.Run{}
	if err := run.Clients.NewClients(ctx, &run.Info); err != nil {
		return nil, E2EOptions{}, bitbucketcloud.Provider{}, err
	}
	e2eoptions := E2EOptions{
		Organization: splitted[0],
		Repo:         splitted[1],
	}
	bbc := bitbucketcloud.Provider{}
	infopts := &info.PacOpts{
		ProviderToken: bitbucketCloudToken,
		ProviderURL:   bitbucketCloudAPIURL,
		ProviderUser:  bitbucketCloudUser,
	}
	if err := bbc.SetClient(ctx, infopts); err != nil {
		return nil, E2EOptions{}, bitbucketcloud.Provider{}, err
	}
	return run, e2eoptions, bbc, nil
}

func githubSetup(ctx context.Context, viaDirectWebhook bool) (*params.Run, E2EOptions, github.Provider, error) {
	githubURL := os.Getenv("TEST_GITHUB_API_URL")
	githubToken := os.Getenv("TEST_GITHUB_TOKEN")
	githubRepoOwnerGithubApp := os.Getenv("TEST_GITHUB_REPO_OWNER_GITHUBAPP")
	githubRepoOwnerDirectWebhook := os.Getenv("TEST_GITHUB_REPO_OWNER_WEBHOOK")

	for _, value := range []string{
		"EL_URL",
		"GITHUB_API_URL",
		"GITHUB_TOKEN",
		"GITHUB_REPO_OWNER_GITHUBAPP",
		"EL_WEBHOOK_SECRET",
	} {
		if env := os.Getenv("TEST_" + value); env == "" {
			return nil, E2EOptions{}, github.Provider{}, fmt.Errorf("\"TEST_%s\" env variable is required, cannot continue", value)
		}
	}

	var splitted []string
	if !viaDirectWebhook {
		if githubURL == "" || githubToken == "" || githubRepoOwnerGithubApp == "" {
			return nil, E2EOptions{}, github.Provider{}, fmt.Errorf("TEST_GITHUB_API_URL TEST_GITHUB_TOKEN TEST_GITHUB_REPO_OWNER_GITHUBAPP need to be set")
		}
		splitted = strings.Split(githubRepoOwnerGithubApp, "/")
	}
	if viaDirectWebhook {
		if githubURL == "" || githubToken == "" || githubRepoOwnerDirectWebhook == "" {
			return nil, E2EOptions{}, github.Provider{}, fmt.Errorf("TEST_GITHUB_API_URL TEST_GITHUB_TOKEN TEST_GITHUB_REPO_OWNER_WEBHOOK need to be set")
		}
		splitted = strings.Split(githubRepoOwnerDirectWebhook, "/")
	}

	run := &params.Run{}
	if err := run.Clients.NewClients(ctx, &run.Info); err != nil {
		return nil, E2EOptions{}, github.Provider{}, err
	}
	e2eoptions := E2EOptions{Organization: splitted[0], Repo: splitted[1], DirectWebhook: viaDirectWebhook}
	gprovider := github.Provider{}
	if err := gprovider.SetClient(ctx, &info.PacOpts{ProviderToken: githubToken, ProviderURL: githubURL}); err != nil {
		return nil, E2EOptions{}, github.Provider{}, err
	}

	return run, e2eoptions, gprovider, nil
}

func createSecret(ctx context.Context, runcnx *params.Run, secretData map[string]string, targetNamespace,
	secretName string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:   secretName,
			Labels: map[string]string{"app.kubernetes.io/managed-by": "pipelines-as-code"},
		},
	}
	secret.StringData = secretData
	_, err := runcnx.Clients.Kube.CoreV1().Secrets(targetNamespace).Create(ctx, secret, metav1.CreateOptions{})
	return err
}

func TestMain(m *testing.M) {
	rand.Seed(time.Now().UTC().UnixNano())
	v := m.Run()
	os.Exit(v)
}
