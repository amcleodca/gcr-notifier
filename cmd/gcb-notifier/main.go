package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/namsral/flag"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"

	"cloud.google.com/go/pubsub"
	"github.com/google/go-github/github"
	googleoauth "golang.org/x/oauth2/google"
	sourcerepo "google.golang.org/api/sourcerepo/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GCRBuildResolvedRepoSource struct {
	CommitSha string `json:commitSha`
	ProjectId string `json:projectId`
	RepoName  string `json:repoName`
}

type GCRBuildSourceProvenance struct {
	ResolvedRepoSource GCRBuildResolvedRepoSource `json:resolvedRepoSource`
}

type GCRBuildStatus struct {
	Id               string                   `json:Id`
	ProjectId        string                   `json:projectId`
	LogUrl           string                   `json:logUrl`
	SourceProvenance GCRBuildSourceProvenance `json:sourceProvenance`
	Status           string                   `json:status`
}

type GithubStatusUpdater interface {
	CreateStatus(context.Context, string, string, string, *github.RepoStatus) (*github.RepoStatus, *github.Response, error)
}

func main() {
	var gcpProjectID string
	var githubAccessToken string
	var subscriptionName string
	var topicName string
	flag.StringVar(&githubAccessToken, "github-access-token", "", "Github Accss token")
	flag.StringVar(&gcpProjectID, "project-id", "", "GCP Project ID")
	flag.StringVar(&topicName, "build-topic", "cloud-builds", "GCP Cloud Build topic")
	flag.StringVar(&subscriptionName, "subscription", "github-status-pusher", "Pub/Sub subscription name")

	flag.Parse()

	if "" == githubAccessToken {
		log.Fatal("a mandatory field (github-access-token) is unspecified or empty.")
	}
	if "" == gcpProjectID {
		log.Fatal("a mandatory field (project-id) is unspecified or empty.")
	}

	github, err := newGithubClient(githubAccessToken)
	if err != nil {
		log.WithError(err).Fatal("Failed to create Github client.")
	}

	sub := newPubSubSubscription(gcpProjectID, topicName, subscriptionName)

	var mu sync.Mutex
	err = sub.Receive(context.Background(), func(ctx context.Context, msg *pubsub.Message) {
		mu.Lock()
		defer mu.Unlock()

		publishStatus(msg.Data, github.Repositories)
		msg.Ack()
	})
}

func newPubSubSubscription(project string, topicName string, subscriptionName string) *pubsub.Subscription {
	client, err := pubsub.NewClient(context.Background(), project)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	_, err = client.CreateSubscription(context.Background(), subscriptionName,
		pubsub.SubscriptionConfig{Topic: client.Topic(topicName)})
	if codes.AlreadyExists == status.Code(err) {
		log.WithFields(log.Fields{
			"subscription": subscriptionName,
			"topic":        topicName,
		}).Info("Subscription already exists.")
	} else if err != nil {
		log.WithError(err).Fatalf("Failed to create subscription: %#v", status.Code(err))
	}

	return client.Subscription(subscriptionName)

}
func newGithubClient(token string) (*github.Client, error) {
	if token == "" {
		log.Errorf("Github Token is empty string")
		return nil, errors.New("Token unspecified")
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(context.Background(), ts)

	return github.NewClient(tc), nil

}

func publishStatus(update []byte, client GithubStatusUpdater) {
	var buildStatus GCRBuildStatus
	json.Unmarshal(update, &buildStatus)
	if buildStatus.Id == "" {
		log.Errorf("Failed to unmarshal GCR Build Status: %s", update)
		return
	}

	l := log.WithFields(log.Fields{
		"Id": buildStatus.Id,
	})

	repoSource := buildStatus.SourceProvenance.ResolvedRepoSource
	l.WithFields(log.Fields{
		"status":  buildStatus.Status,
		"project": buildStatus.ProjectId,
		"sha":     repoSource.CommitSha,
		"repo":    repoSource.RepoName,
	}).Infof("got GCR build update")

	repoURL, err := getSourceRepoMirrorURL(repoSource.ProjectId, repoSource.RepoName)
	if err != nil {
		l.WithError(err).WithFields(log.Fields{
			"sourceProject": repoSource.ProjectId,
			"sourceRepo":    repoSource.RepoName,
		}).Error("Failed to get repo URL from source repo")
		return
	}

	owner, repo, err := getRepoIdentityFromURL(repoURL)
	if err != nil {
		l.WithError(err).WithField("url", repoURL).Error("Failed to get origin repo from source repo URL")
		return
	}

	githubStatus := func(buildStatus *GCRBuildStatus) *github.RepoStatus {
		status := map[string]string{
			"CANCELLED":      "error",
			"FAILURE":        "failure",
			"INTERNAL_ERROR": "error",
			"QUEUED":         "pending",
			"STATUS_UNKNOWN": "error",
			"SUCCESS":        "success",
			"TIMEOUT":        "failure",
			"WORKING":        "pending",
		}[buildStatus.Status]
		if status == "" {
			status = "unknown"
			l.Warnf("Unhandled status code: %s", status)
		}

		context := "Google Container Builder"
		description := "Build"
		return &github.RepoStatus{
			Context:     &context,
			Description: &description,
			State:       &status,
			TargetURL:   &buildStatus.LogUrl,
		}
	}(&buildStatus)

	updateLog := log.Fields{
		"owner":       owner,
		"repo":        repo,
		"State":       *githubStatus.State,
		"Description": *githubStatus.Description,
		"TargetURL":   *githubStatus.TargetURL,
	}
	_, _, err = client.CreateStatus(context.Background(),
		owner, repo, repoSource.CommitSha, githubStatus)
	if err != nil {
		l.WithError(err).WithFields(updateLog).Error("Failed to push update to github")
	} else {
		l.WithFields(updateLog).Info("sent")
	}
}

func getSourceRepoMirrorURL(projectId string, repoName string) (string, error) {
	oauth, err := googleoauth.DefaultClient(context.TODO(), sourcerepo.SourceReadOnlyScope)
	if nil != err {
		return "", fmt.Errorf("Failed to create google auth client: %s", err.Error())
	}
	client, err := sourcerepo.New(oauth)
	if err != nil {
		return "", fmt.Errorf("Failed to create source repo client: %s", err.Error())
	}

	fullRepoName := fmt.Sprintf("projects/%s/repos/%s", projectId, repoName)
	repo, err := client.Projects.Repos.Get(fullRepoName).Do()
	if err != nil {
		return "", fmt.Errorf("Failed to get repo %s: %s", fullRepoName, err.Error())
	}
	if repo.MirrorConfig == nil {
		return repo.Url, nil
	}

	return repo.MirrorConfig.Url, nil
}

func getRepoIdentityFromURL(repoURL string) (string, string, error) {
	u, err := url.Parse(repoURL)
	if err != nil {
		return "", "", fmt.Errorf("Failed to parse URL: %s: %s", repoURL, err.Error())
	}

	if u.Hostname() != "github.com" {
		return "", "", fmt.Errorf("Unknown repo provider for %s", repoURL)
	}
	path := strings.Split(u.Path, "/")
	if len(path) != 3 {
		return "", "", fmt.Errorf("path too short for %s", repoURL)
	}
	owner, repo := path[1], strings.TrimSuffix(path[2], ".git")

	return owner, repo, nil
}
