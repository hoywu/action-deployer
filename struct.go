package main

import "time"

type Artifacts struct {
	TotalCount int64      `json:"total_count"`
	Artifacts  []Artifact `json:"artifacts"`
}

type Artifact struct {
	ID                 int64       `json:"id"`
	NodeID             string      `json:"node_id"`
	Name               string      `json:"name"`
	SizeInBytes        int64       `json:"size_in_bytes"`
	URL                string      `json:"url"`
	ArchiveDownloadURL string      `json:"archive_download_url"`
	Expired            bool        `json:"expired"`
	CreatedAt          time.Time   `json:"created_at"`
	UpdatedAt          time.Time   `json:"updated_at"`
	ExpiresAt          time.Time   `json:"expires_at"`
	WorkflowRun        WorkflowRun `json:"workflow_run"`
}

type WorkflowRun struct {
	ID               int64  `json:"id"`
	RepositoryID     int64  `json:"repository_id"`
	HeadRepositoryID int64  `json:"head_repository_id"`
	HeadBranch       string `json:"head_branch"`
	HeadSHA          string `json:"head_sha"`
}
