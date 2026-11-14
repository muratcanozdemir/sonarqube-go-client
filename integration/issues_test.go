//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	sq "github.com/muratcanozdemir/sonarqube-go-client/sonarqube"
)

func scannedIssues(t *testing.T) []*sq.IssuesSearchResponseIssue {
	t.Helper()
	res := ok(c.Issues.Search(ctx, &sq.IssuesSearchOptions{Components: []string{scanned}, AdditionalFields: []string{"_all"}, Facets: []string{"rules", "severities"}, Ps: sq.Ptr(100)})).must(t)
	if len(res.Issues) < 2 {
		t.Fatalf("scanned project has %d issues", len(res.Issues))
	}
	return res.Issues
}

func TestIssues(t *testing.T) {
	issues := scannedIssues(t)
	key := issues[0].Key

	cm := ok(c.Issues.AddComment(ctx, &sq.IssuesAddCommentOptions{Issue: key, Text: "it comment"})).must(t)
	var commentKey string
	for _, cmt := range cm.Issue.Comments {
		commentKey = cmt.Key
	}
	ok(c.Issues.EditComment(ctx, &sq.IssuesEditCommentOptions{Comment: commentKey, Text: "it comment edited"})).must(t)
	ok(c.Issues.DeleteComment(ctx, &sq.IssuesDeleteCommentOptions{Comment: commentKey})).must(t)

	ok(c.Issues.Assign(ctx, &sq.IssuesAssignOptions{Issue: key, Assignee: "admin"})).must(t)
	ok(c.Issues.Assign(ctx, &sq.IssuesAssignOptions{Issue: key})).must(t)
	ok(c.Issues.SetTags(ctx, &sq.IssuesSetTagsOptions{Issue: key, Tags: []string{"it-tag"}})).must(t)
	ok(c.Issues.Tags(ctx, &sq.IssuesTagsOptions{Project: scanned})).must(t)
	ok(c.Issues.ComponentTags(ctx, &sq.IssuesComponentTagsOptions{ComponentUUID: projectUUID(t)})).must(t)
	ok(c.Issues.Authors(ctx, &sq.IssuesAuthorsOptions{Project: scanned})).must(t)
	// Severity and type are only editable in Standard Experience mode; MQR
	// mode (the default on recent versions) rejects them with 400.
	ok(c.Issues.SetSeverity(ctx, &sq.IssuesSetSeverityOptions{Issue: key, Severity: "MINOR"})).allow(t, 400)
	ok(c.Issues.SetType(ctx, &sq.IssuesSetTypeOptions{Issue: key, Type: "CODE_SMELL"})).allow(t, 400)

	ok(c.Issues.DoTransition(ctx, &sq.IssuesDoTransitionOptions{Issue: key, Transition: "accept"})).must(t)
	ok(c.Issues.DoTransition(ctx, &sq.IssuesDoTransitionOptions{Issue: key, Transition: "reopen"})).must(t)
	cl := ok(c.Issues.Changelog(ctx, &sq.IssuesChangelogOptions{Issue: key})).must(t)
	if len(cl.Changelog) == 0 {
		t.Error("empty changelog")
	}
	ok(c.Issues.BulkChange(ctx, &sq.IssuesBulkChangeOptions{Issues: []string{issues[0].Key, issues[1].Key}, AddTags: []string{"bulk"}, Comment: "bulk"})).must(t)
	ok(c.Issues.List(ctx, &sq.IssuesListOptions{Project: scanned})).must(t)
	if b := ok(c.Issues.Pull(ctx, &sq.IssuesPullOptions{ProjectKey: scanned, BranchName: mainBranch(t)})).must(t); len(b) == 0 {
		t.Error("empty pull stream")
	}
	ok(c.Issues.PullTaint(ctx, &sq.IssuesPullTaintOptions{ProjectKey: scanned, BranchName: mainBranch(t)})).must(t)
	done(c.Issues.AnticipatedTransitions(ctx, &sq.IssuesAnticipatedTransitionsOptions{ProjectKey: scanned, Body: []*sq.AnticipatedTransition{{
		RuleKey: "python:S1481", IssueMessage: "Remove the unused local variable", FilePath: "src/app.py", Line: 25, Transition: "accept", Comment: "it",
	}}})).must(t)
	ok(c.Sources.IssueSnippets(ctx, &sq.SourcesIssueSnippetsOptions{IssueKey: key})).must(t)
	done(c.Issues.Reindex(ctx, &sq.IssuesReindexOptions{Project: scanned})).must(t)
	waitIndexed(t)
}

// waitIndexed waits until issue indexing (a background task triggered by
// reindex) has finished, polling with backoff.
func waitIndexed(t *testing.T) {
	t.Helper()
	wctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := c.WaitForEmptyQueue(wctx, sq.DefaultPollPolicy); err != nil {
		t.Fatalf("issue indexing did not finish: %v", err)
	}
}

func projectUUID(t *testing.T) string {
	t.Helper()
	nav := ok(c.Navigation.Component(ctx, &sq.NavigationComponentOptions{Component: scanned})).must(t)
	return nav.ID.String()
}

func mainBranch(t *testing.T) string {
	t.Helper()
	bs := ok(c.ProjectBranches.List(ctx, &sq.ProjectBranchesListOptions{Project: scanned})).must(t)
	for _, b := range bs.Branches {
		if b.IsMain {
			return b.Name
		}
	}
	t.Fatal("no main branch")
	return ""
}

func TestHotspots(t *testing.T) {
	res := ok(c.Hotspots.Search(ctx, &sq.HotspotsSearchOptions{Project: scanned})).must(t)
	ok(c.Hotspots.List(ctx, &sq.HotspotsListOptions{Project: scanned})).must(t)
	ok(c.Hotspots.Pull(ctx, &sq.HotspotsPullOptions{ProjectKey: scanned, BranchName: mainBranch(t)})).must(t)
	if available("Hotspots.MigrationStatus") {
		ok(c.Hotspots.MigrationStatus(ctx, nil)).must(t)
		done(c.Hotspots.MigrateToIssues(ctx, nil)).allow(t, 400, 409)
	}
	if len(res.Hotspots) == 0 {
		// From 26.9 security hotspots are reported as issues, so the
		// project has none: drive the hotspot endpoints with an unknown key.
		const missing = "it-no-such-hotspot"
		ok(c.Hotspots.Show(ctx, &sq.HotspotsShowOptions{Hotspot: missing})).allow(t, 404)
		done(c.Hotspots.Assign(ctx, &sq.HotspotsAssignOptions{Hotspot: missing, Assignee: "admin"})).allow(t, 404)
		done(c.Hotspots.ChangeStatus(ctx, &sq.HotspotsChangeStatusOptions{Hotspot: missing, Status: "REVIEWED", Resolution: "SAFE"})).allow(t, 404)
		done(c.Hotspots.AddComment(ctx, &sq.HotspotsAddCommentOptions{Hotspot: missing, Comment: "x"})).allow(t, 404)
		ok(c.Hotspots.EditComment(ctx, &sq.HotspotsEditCommentOptions{Comment: missing, Text: "x"})).allow(t, 404)
		done(c.Hotspots.DeleteComment(ctx, &sq.HotspotsDeleteCommentOptions{Comment: missing})).allow(t, 404)
		return
	}
	hs := res.Hotspots[0].Key
	ok(c.Hotspots.Show(ctx, &sq.HotspotsShowOptions{Hotspot: hs})).must(t)
	done(c.Hotspots.Assign(ctx, &sq.HotspotsAssignOptions{Hotspot: hs, Assignee: "admin"})).must(t)
	done(c.Hotspots.AddComment(ctx, &sq.HotspotsAddCommentOptions{Hotspot: hs, Comment: "it"})).must(t)
	show := ok(c.Hotspots.Show(ctx, &sq.HotspotsShowOptions{Hotspot: hs})).must(t)
	if len(show.Comment) == 0 {
		t.Fatal("comment not stored")
	}
	cmt := show.Comment[len(show.Comment)-1].Key
	ok(c.Hotspots.EditComment(ctx, &sq.HotspotsEditCommentOptions{Comment: cmt, Text: "it edited"})).must(t)
	done(c.Hotspots.DeleteComment(ctx, &sq.HotspotsDeleteCommentOptions{Comment: cmt})).must(t)
	done(c.Hotspots.ChangeStatus(ctx, &sq.HotspotsChangeStatusOptions{Hotspot: hs, Status: "REVIEWED", Resolution: "SAFE"})).must(t)
	done(c.Hotspots.ChangeStatus(ctx, &sq.HotspotsChangeStatusOptions{Hotspot: hs, Status: "TO_REVIEW"})).must(t)
}

func TestRules(t *testing.T) {
	rs := ok(c.Rules.Search(ctx, &sq.RulesSearchOptions{Languages: []string{"py"}, Facets: []string{"types"}, Ps: sq.Ptr(5)})).must(t)
	if len(rs.Rules) == 0 {
		t.Fatal("no python rules")
	}
	ok(c.Rules.Show(ctx, &sq.RulesShowOptions{Key: rs.Rules[0].Key, Actives: sq.Ptr(true)})).must(t)
	ok(c.Rules.Repositories(ctx, nil)).must(t)
	ok(c.Rules.Tags(ctx, nil)).must(t)
	ok(c.Rules.App(ctx)).must(t)
	ok(c.Rules.List(ctx, &sq.RulesListOptions{Ps: sq.Ptr(5)})).must(t)

	tpl := ok(c.Rules.Search(ctx, &sq.RulesSearchOptions{IsTemplate: sq.Ptr(true), Languages: []string{"py"}, Ps: sq.Ptr(1)})).must(t)
	if len(tpl.Rules) == 0 {
		t.Fatal("no python template rule")
	}
	custom := ok(c.Rules.Create(ctx, &sq.RulesCreateOptions{
		CustomKey: uniq("it_rule"), Name: uniq("it rule"), MarkdownDescription: "desc", TemplateKey: tpl.Rules[0].Key,
		Status: "READY",
	})).must(t)
	ok(c.Rules.Update(ctx, &sq.RulesUpdateOptions{Key: custom.Rule.Key, Name: uniq("it rule 2"), MarkdownDescription: "desc2"})).must(t)
	done(c.Rules.Delete(ctx, &sq.RulesDeleteOptions{Key: custom.Rule.Key})).must(t)

	// v2 custom rule creation.
	created := ok(c.CleanCodePolicy.CreateRule(ctx, &sq.CleanCodePolicyCreateRuleOptions{Body: &sq.RuleCreateRestRequest{
		Key: "python:" + strings.ReplaceAll(uniq("it_rule_v2"), "-", "_"), TemplateKey: tpl.Rules[0].Key, Name: uniq("it rule v2"), MarkdownDescription: "desc",
		Status: sq.Ptr("READY"), Impacts: []*sq.Impact{{SoftwareQuality: sq.Ptr("MAINTAINABILITY"), Severity: sq.Ptr("LOW")}},
	}})).must(t)
	done(c.Rules.Delete(ctx, &sq.RulesDeleteOptions{Key: created.Key})).must(t)

	mode := ok(c.CleanCodePolicy.GetMode(ctx)).must(t)
	ok(c.CleanCodePolicy.UpdateMode(ctx, &sq.CleanCodePolicyUpdateModeOptions{Body: &sq.ModeResource{Mode: mode.Mode}})).must(t)
}
