package main

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
