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

type GCRBuildRepoSource struct {
	BranchName string `json:branchName`
	ProjectId  string `json:projectId`
	RepoName   string `json:repoName`
}

type GCRBuildResolvedRepoSource struct {
	CommitSha string `json:commitSha`
	ProjectId string `json:projectId`
	RepoName  string `json:repoName`
}

// XXX This is not unmarshaling correctly
type GCRBuildSource struct {
	RepoSource GCRBuildRepoSource `json:repoSource`
}

type GCRBuildSourceProvenance struct {
	ResolvedRepoSource GCRBuildResolvedRepoSource `json:resolvedRepoSource`
}
type GCRBuildStep struct {
	Args []string `json:args`
	Name string   `json:name`
	Env  []string `json:env`
}

type GCRBuildStatus struct {
	Id               string                   `json:Id`
	ProjectId        string                   `json:projectId`
	CreateTime       string                   `json:createTime`
	LogUrl           string                   `json:logUrl`
	LogsBucket       string                   `json:logsBucket`
	Source           GCRBuildRepoSource       `json:source`
	SourceProvenance GCRBuildSourceProvenance `json:sourceProvenance`
	Status           string                   `json:status`
	Steps            []GCRBuildStep           `json:steps`
	//Tags
	Timeout string `json:timeout`
}

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

func MakeGithubStatusFromGCR(status *GCRBuildStatus) (*github.RepoStatus, error) {
	var gstate string
	switch status.Status {
	case "QUEUED":
		gstate = "pending"
	case "WORKING":
		gstate = "pending"
	case "TIMEOUT":
		gstate = "failure"
	case "STATUS_UNKNOWN":
		gstate = "error"
	case "SUCCESS":
		gstate = "success"
	case "FAILURE":
		gstate = "failure"
	case "INTERNAL_ERROR":
		gstate = "error"
	case "CANCELLED":
		gstate = "error"
	default:
		return nil, errors.New(fmt.Sprintf("Unknown build state: %s", status.Status))
	}

	return &github.RepoStatus{
		State:     &gstate,
		TargetURL: &status.LogUrl,
		// Description: "nyi",
		// Context:     "GCR",
	}, nil
}

func main() {

	/// Auth With Github
	githubToken := os.Getenv("GITHUB_ACCESS_TOKEN")
	if githubToken == "" {
		log.Fatalf("Github Token is empty string")
	}
	githubctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: githubToken},
	)
	tc := oauth2.NewClient(githubctx, ts)

	ghclient := github.NewClient(tc)

	// Auth with Google
	ctx := context.Background()

	// Sets your Google Cloud Platform project ID.
	projectID := "amcleodca-fuzz"

	// Creates a client.
	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	// Sets the name for the new topic.
	topicName := "cloud-builds"

	subscriptionName := "github-status-pusher"
	// Creates the new topic.
	topic, err := client.CreateSubscription(context.Background(), subscriptionName, pubsub.SubscriptionConfig{Topic: client.Topic(topicName)})
	if codes.AlreadyExists == status.Code(err) {
		log.WithError(err).Warnf("Subscription already exists.")

	} else if err != nil {
		log.WithError(err).Fatalf("Failed to create subscription: %#v", status.Code(err))
	}

	// START RECEIVING
	var mu sync.Mutex
	received := 0
	sub := client.Subscription(subscriptionName)
	cctx, cancel := context.WithCancel(ctx)
	err = sub.Receive(cctx, func(ctx context.Context, msg *pubsub.Message) {
		mu.Lock()
		defer mu.Unlock()
		received++
		if received >= 1024 {
			cancel()
			msg.Nack()
			return
		}
		var buildStatus GCRBuildStatus
		json.Unmarshal(msg.Data, &buildStatus)
		log.Infof("Got message: %+v", buildStatus)
		githubStatus, err := MakeGithubStatusFromGCR(&buildStatus)
		if err != nil {
			log.WithError(err).Warnf("Failed to make github status")
		}
		log.Infof("Github Status: %+v", githubStatus)
		owner, repo, sha, err := GetGithubUrlFromStatus(&buildStatus)
		if err != nil {
			log.WithError(err).Errorf("Failed to get URL from build status")
		}

		ghclient.Repositories.CreateStatus(context.Background(), owner, repo, sha, githubStatus)

		msg.Ack()
	})
	// END RECEIVER

	fmt.Printf("Topic %v created.\n", topic)
}
