package api

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mdwiki/internal/indexbuilder"
	"mdwiki/internal/metadata"
)

func spaceInitialized(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".mdwiki", "index.json"))
	return err == nil
}

func spaceHasWikiDir(root string) bool {
	info, err := os.Stat(filepath.Join(root, ".mdwiki"))
	return err == nil && info.IsDir()
}

func ensureInitialized(root, spaceKey string) (*indexbuilder.IndexDoc, error) {
	doc, err := indexbuilder.ScanMarkdown(root, spaceKey)
	if err != nil {
		return nil, err
	}
	if err := indexbuilder.WriteIndex(root, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func normalizeRepoRelPath(raw string) (string, error) {
	rel := strings.TrimSpace(filepath.ToSlash(raw))
	if rel == "" {
		return "", fmt.Errorf("path is required")
	}
	if strings.HasPrefix(rel, "/") || strings.Contains(rel, "../") {
		return "", fmt.Errorf("invalid path")
	}
	return rel, nil
}

func pageMetadataForPath(root, pagePath string, useMetadata bool) (*metadata.Doc, error) {
	if !useMetadata {
		return nil, nil
	}
	doc, err := metadata.Read(root, pagePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return doc, nil
}

func pageIDForPath(root, pagePath string) string {
	doc, err := indexbuilder.LoadIndex(root)
	if err != nil {
		return ""
	}
	for _, page := range doc.Pages {
		if page.Path == pagePath {
			return page.PageID
		}
	}
	return ""
}

var taskLineRE = regexp.MustCompile(`^\s*-\s+\[( |x|X)\]\s+(.*?)(?:\s+_\((.*)\)_)?\s*$`)

type extractedTask struct {
	Text        string `json:"text"`
	Checked     bool   `json:"checked"`
	Assignee    string `json:"assignee,omitempty"`
	Priority    string `json:"priority,omitempty"`
	DueDate     string `json:"due_date,omitempty"`
	Collectable bool   `json:"collectable"`
	SourcePath  string `json:"source_path"`
}

func extractTasks(markdown, sourcePath string) []extractedTask {
	scanner := bufio.NewScanner(strings.NewReader(markdown))
	var tasks []extractedTask
	inBlock := false
	for scanner.Scan() {
		line := scanner.Text()
		switch strings.TrimSpace(line) {
		case "<!-- wiki:tasks:start -->":
			inBlock = true
			continue
		case "<!-- wiki:tasks:end -->":
			inBlock = false
			continue
		}
		if !inBlock {
			continue
		}
		m := taskLineRE.FindStringSubmatch(line)
		if len(m) == 0 {
			continue
		}
		task := extractedTask{
			Text:        strings.TrimSpace(m[2]),
			Checked:     strings.EqualFold(m[1], "x"),
			Collectable: true,
			SourcePath:  sourcePath,
		}
		for _, part := range strings.Split(m[3], ",") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "assignee:") {
				task.Assignee = strings.TrimSpace(strings.TrimPrefix(part, "assignee:"))
			}
			if strings.HasPrefix(part, "priority:") {
				task.Priority = strings.TrimSpace(strings.TrimPrefix(part, "priority:"))
			}
			if strings.HasPrefix(part, "due:") {
				task.DueDate = strings.TrimSpace(strings.TrimPrefix(part, "due:"))
			}
		}
		tasks = append(tasks, task)
	}
	return tasks
}

var headingRE = regexp.MustCompile(`^\s*#{1,6}\s+(.*?)\s*$`)

func extractHeadings(markdown string) []string {
	scanner := bufio.NewScanner(strings.NewReader(markdown))
	var out []string
	for scanner.Scan() {
		line := scanner.Text()
		m := headingRE.FindStringSubmatch(line)
		if len(m) == 0 {
			continue
		}
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}
