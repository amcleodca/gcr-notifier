package main

import (
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus"
	"os"
	"strings"
	"sync"

	"encoding/json"

	"cloud.google.com/go/pubsub"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

// returns owner, repo, sha, error
func GetGithubUrlFromStatus(status *GCRBuildStatus) (string, string, string, error) {
	fields := strings.Split(status.SourceProvenance.ResolvedRepoSource.RepoName, "-")
	if len(fields) < 3 {
		return "", "", "", errors.New(fmt.Sprintf("Failed to parse github URL from %s", status.SourceProvenance.ResolvedRepoSource.RepoName))
	}
	if fields[1] != "amcleodca" {
		panic("Illegal repo owner")
	}

	return fields[1], strings.Join(fields[2:], "-"), status.SourceProvenance.ResolvedRepoSource.CommitSha, nil
}

var GCRGithubStatus = map[string]string{
	"QUEUED":         "pending",
	"WORKING":        "pending",
	"TIMEOUT":        "failure",
	"STATUS_UNKNOWN": "error",
	"SUCCESS":        "success",
	"FAILURE":        "failure",
	"INTERNAL_ERROR": "error",
	"CANCELLED":      "error",
}

func MakeGithubStatusFromGCR(status *GCRBuildStatus) (*github.RepoStatus, error) {
	gstatus := GCRGithubStatus[status.Status]
	if gstatus == "" {
		gstatus = "unknown"
	}

	gcontext := "Google Container Builder"
	gdescription := "Description" // XXX
	return &github.RepoStatus{
		State:       &gstatus,
		TargetURL:   &status.LogUrl,
		Description: &gdescription,
		Context:     &gcontext,
	}, nil
}

type GHClient struct {
	client *github.Client
}

func NewGHClient(token string) (*GHClient, error) {
	/// Auth With Github
	if token == "" {
		log.Errorf("Github Token is empty string")
		return nil, errors.New("Token unspecified")
	}

	githubctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(githubctx, ts)

	return &GHClient{
		client: github.NewClient(tc),
	}, nil

}

func main() {
	githubToken := os.Getenv("GITHUB_ACCESS_TOKEN")
	ghclient, err := NewGHClient(githubToken)
	if err != nil {
		log.WithError(err).Fatal("Failed to create Github client.")
	}

	// Sets your Google Cloud Platform project ID.
	projectID := "amcleodca-fuzz"

	// Creates a client.
	client, err := pubsub.NewClient(context.Background(), projectID)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	// Sets the name for the new topic.
	topicName := "cloud-builds"

	subscriptionName := "github-status-pusher"
	// Creates the new topic.
	_, err = client.CreateSubscription(context.Background(), subscriptionName, pubsub.SubscriptionConfig{Topic: client.Topic(topicName)})
	if codes.AlreadyExists == status.Code(err) {
		log.WithError(err).Info("Subscription already exists.")

	} else if err != nil {
		log.WithError(err).Fatalf("Failed to create subscription: %#v", status.Code(err))
	}

	// XXX HOW TO (should we) create the subscription every time?
	// XXX IF not, how do we detect the error where the subscription does not exist?
	var mu sync.Mutex
	received := 0
	sub := client.Subscription(subscriptionName)
	cctx, cancel := context.WithCancel(context.Background())
	err = sub.Receive(cctx, func(ctx context.Context, msg *pubsub.Message) {
		mu.Lock()
		defer mu.Unlock()

		received++
		if received >= 10240 {
			cancel()
			msg.Nack()
			return
		}

		ghclient.publishStatus(msg.Data)

		msg.Ack()
	})
}

func (gh GHClient) publishStatus(update []byte) {
	var buildStatus GCRBuildStatus
	json.Unmarshal(update, &buildStatus)
	// XXX detect and log unmarshal failure

	log.WithFields(log.Fields{
		"status":  buildStatus.Status,
		"project": buildStatus.ProjectId,
		"sha":     buildStatus.SourceProvenance.ResolvedRepoSource.CommitSha,
		"repo":    buildStatus.SourceProvenance.ResolvedRepoSource.RepoName,
	}).Infof("got GCR build update")

	githubStatus, err := MakeGithubStatusFromGCR(&buildStatus)
	if err != nil {
		log.WithError(err).Warnf("Failed to make github status")
	}
	log.Infof("Github Status: %+v", githubStatus)
	owner, repo, sha, err := GetGithubUrlFromStatus(&buildStatus)
	if err != nil {
		log.WithError(err).Errorf("Failed to get URL from build status")
	}

	gh.client.Repositories.CreateStatus(context.Background(), owner, repo, sha, githubStatus)

}
