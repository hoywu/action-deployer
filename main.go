package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/twmb/murmur3"
)

type Secret struct {
	Owner string `json:"owner"`
	Token string `json:"token"`
}

type Job struct {
	Owner        string   `json:"owner"`
	Repo         string   `json:"repo"`
	ArtifactName string   `json:"artifactName"`
	Excludes     []string `json:"excludes"`
	DeployPath   string   `json:"deployPath"`
}

const (
	tempDir      = "tmp"
	artifactsDir = "artifacts"

	secretFile = "secret.json"
	jobFile    = "job.json"
	logFile    = "log.json"
)

var (
	secretMap  map[string]string
	jobs       []Job
	lastUpdate map[string]time.Time // Owner.Repo.ArtifactName -> created_at

	client = &http.Client{}
)

func init() {
	// init secret
	secrets := make([]Secret, 0)
	if err := loadJSON(secretFile, &secrets); err != nil {
		log.Fatal(err)
	}
	secretMap = make(map[string]string)
	for _, s := range secrets {
		secretMap[s.Owner] = s.Token
	}

	// init job
	if err := loadJSON(jobFile, &jobs); err != nil {
		log.Fatal(err)
	}

	// init log
	lastUpdate = make(map[string]time.Time)
	_, err := os.Stat(logFile)
	if os.IsNotExist(err) {
		if err := os.WriteFile(logFile, []byte("{}"), 0644); err != nil {
			log.Fatal(err)
		}
	}
	if err := loadJSON(logFile, &lastUpdate); err != nil {
		log.Fatal(err)
	}

	// init directory structure
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(artifactsDir, 0755); err != nil {
		log.Fatal(err)
	}
}

func main() {
	for {
		runJobs()
		time.Sleep(5 * time.Minute)
	}
}

func runJobs() {
	for _, j := range jobs {
		runJob(j)
	}
}

func runJob(j Job) {
	key := fmt.Sprintf("%v.%v.%v", j.Owner, j.Repo, j.ArtifactName)
	log.Printf("[Info] Running job: %v\n", key)

	artifact, err := getLatestArtifact(j)
	if err != nil {
		log.Printf("[Error] Job %v: %v\n", key, err)
		return
	}

	if artifact.CreatedAt.Equal(lastUpdate[key]) {
		return
	}
	markUpdate(key, artifact.CreatedAt)

	if err := downloadArtifact(j.Owner, artifact, key); err != nil {
		log.Printf("[Error] Job %v: %v\n", key, err)
		return
	}

	if err := unzipDiff(
		filepath.Join(artifactsDir, key+".zip"),
		j.DeployPath,
		j.Excludes,
	); err != nil {
		log.Printf("[Error] Job %v: %v\n", key, err)
		return
	}
}

func markUpdate(key string, t time.Time) {
	lastUpdate[key] = t
	if err := saveJSON(logFile, lastUpdate); err != nil {
		log.Fatal(err)
	}
}

func getLatestArtifact(j Job) (*Artifact, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/artifacts", j.Owner, j.Repo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+secretMap[j.Owner])
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	as := new(Artifacts)
	if err := json.NewDecoder(resp.Body).Decode(as); err != nil {
		return nil, err
	}

	// sort by created_at
	slices.SortFunc(as.Artifacts, func(i, j Artifact) int {
		return j.CreatedAt.Compare(i.CreatedAt)
	})
	// only return the artifact with correct name
	for i := 0; i < len(as.Artifacts); i++ {
		if as.Artifacts[i].Name == j.ArtifactName {
			return &as.Artifacts[i], nil
		}
	}

	return nil, fmt.Errorf("no artifact found")
}

func downloadArtifact(owner string, a *Artifact, filename string) error {
	url := a.ArchiveDownloadURL
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+secretMap[owner])
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// write to file
	file, err := os.CreateTemp(tempDir, "artifact-tmp-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, resp.Body); err != nil {
		return err
	}
	file.Close()

	return os.Rename(file.Name(), filepath.Join(artifactsDir, filename+".zip"))
}

func unzipDiff(filename string, dest string, excludes []string) error {
	r, err := zip.OpenReader(filename)
	if err != nil {
		return err
	}
	defer r.Close()

	wg := sync.WaitGroup{}
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if pathMatches(f.Name, excludes) {
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := extractDiff(f, dest); err != nil {
				log.Printf("[Error] Extract %v: %v\n", f.Name, err)
			}
		}()
	}
	wg.Wait()
	return nil
}

func extractDiff(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	b := &bytes.Buffer{}
	if _, err := io.Copy(b, rc); err != nil {
		return err
	}
	if err := rc.Close(); err != nil {
		return err
	}

	path := filepath.Join(dest, f.Name)

	// Check for ZipSlip (Directory traversal)
	if !strings.HasPrefix(path, filepath.Clean(dest)+string(os.PathSeparator)) {
		return fmt.Errorf("illegal file path: %s", path)
	}

	if diff, err := hasDiff(b, path); err != nil {
		return err
	} else if !diff {
		// log.Printf("[Info] No diff: %v\n", f.Name)
		return nil
	}
	log.Printf("[Info] Extracting: %v\n", f.Name)

	os.MkdirAll(filepath.Dir(path), 0755)
	t, err := os.CreateTemp(tempDir, "extract-*")
	if err != nil {
		return err
	}
	if _, err = io.Copy(t, b); err != nil {
		return err
	}
	if err := t.Close(); err != nil {
		return err
	}
	if err := os.Rename(t.Name(), path); err != nil {
		return err
	}

	return nil
}

func hasDiff(b *bytes.Buffer, destFile string) (bool, error) {
	// MurMurHash3 128-bit
	mb := murmur3.New128()
	if _, err := mb.Write(b.Bytes()); err != nil {
		return false, err
	}
	hashb := mb.Sum(nil) // result 1

	f, err := os.Open(destFile)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	defer f.Close()

	fb := murmur3.New128()
	if _, err := io.Copy(fb, f); err != nil {
		return false, err
	}
	hashf := fb.Sum(nil) // result 2

	return !bytes.Equal(hashb, hashf), nil
}

func pathMatches(p string, excludes []string) bool {
	for _, e := range excludes {
		if ok, _ := regexp.MatchString("^"+e+"$", p); ok {
			return true
		}
	}
	return false
}

func loadJSON(filename string, v any) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	return json.NewDecoder(file).Decode(v)
}

func saveJSON(filename string, v any) error {
	file, err := os.CreateTemp(tempDir, "tmp-*")
	if err != nil {
		return err
	}
	if err := json.NewEncoder(file).Encode(v); err != nil {
		return err
	}
	file.Close()

	return os.Rename(file.Name(), filename)
}
