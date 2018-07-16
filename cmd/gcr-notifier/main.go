package main

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/namsral/flag"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"

	"cloud.google.com/go/pubsub"
	"github.com/google/go-github/github"
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

	/* The repo appears to be specified in the form: github-<ORGANIZATION>-<REPOSITORY>,
	   for example: "github-amcleodca-gcr-notifier"
	*/
	fields := strings.Split(repoSource.RepoName, "-")
	if len(fields) < 3 {
		log.Errorf("Failed to parse github info from %s",
			repoSource.RepoName)
		return
	}
	if fields[0] != "github" {
		log.Errorf("Unknown repo type: %s", fields[0])
		return
	}

	owner := fields[1]
	repo := strings.Join(fields[2:], "-")

	_, _, err := client.CreateStatus(context.Background(),
		owner, repo, repoSource.CommitSha, githubStatus)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"owner":       owner,
			"repo":        repo,
			"State":       githubStatus.State,
			"Description": githubStatus.Description,
			"TargetURL":   githubStatus.TargetURL,
		}).Error("Failed to push update to github")
	}

}
