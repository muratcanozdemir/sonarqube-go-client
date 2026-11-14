//go:build integration

package integration

import (
	"strings"
	"testing"

	sq "github.com/muratcanozdemir/sonarqube-go-client/sonarqube"
)

// newProject creates a throwaway project and deletes it after the test.
func newProject(t *testing.T, prefix string) string {
	t.Helper()
	key := uniq(prefix)
	p := ok(c.Projects.Create(ctx, &sq.ProjectsCreateOptions{Project: key, Name: key, Visibility: "private"})).must(t)
	if p.Project == nil || p.Project.Key != key {
		t.Fatalf("created %+v", p)
	}
	t.Cleanup(func() { _, _ = c.Projects.Delete(ctx, &sq.ProjectsDeleteOptions{Project: key}) })
	return key
}

func TestProjects(t *testing.T) {
	key := newProject(t, "it-proj")
	found := ok(c.Projects.Search(ctx, &sq.ProjectsSearchOptions{Projects: []string{key}})).must(t)
	if len(found.Components) != 1 {
		t.Fatalf("search = %+v", found)
	}
	ok(c.Projects.SearchMyProjects(ctx, nil)).must(t)
	ok(c.Projects.SearchMyScannableProjects(ctx, nil)).must(t)
	done(c.Projects.UpdateVisibility(ctx, &sq.ProjectsUpdateVisibilityOptions{Project: key, Visibility: "public"})).must(t)
	done(c.Projects.UpdateDefaultVisibility(ctx, &sq.ProjectsUpdateDefaultVisibilityOptions{ProjectVisibility: "public"})).must(t)

	newKey := key + "-renamed"
	done(c.Projects.UpdateKey(ctx, &sq.ProjectsUpdateKeyOptions{From: key, To: newKey})).must(t)
	t.Cleanup(func() { _, _ = c.Projects.Delete(ctx, &sq.ProjectsDeleteOptions{Project: newKey}) })

	ok(c.ProjectTags.Search(ctx, nil)).must(t)
	done(c.ProjectTags.Set(ctx, &sq.ProjectTagsSetOptions{Project: newKey, Tags: []string{"it", "go"}})).must(t)

	link := ok(c.ProjectLinks.Create(ctx, &sq.ProjectLinksCreateOptions{ProjectKey: newKey, Name: "home", URL: "https://example.com"})).must(t)
	links := ok(c.ProjectLinks.Search(ctx, &sq.ProjectLinksSearchOptions{ProjectKey: newKey})).must(t)
	if len(links.Links) == 0 {
		t.Error("no links")
	}
	done(c.ProjectLinks.Delete(ctx, &sq.ProjectLinksDeleteOptions{ID: link.Link.ID.String()})).must(t)

	done(c.Favorites.Add(ctx, &sq.FavoritesAddOptions{Component: newKey})).must(t)
	ok(c.Favorites.Search(ctx, nil)).must(t)
	done(c.Favorites.Remove(ctx, &sq.FavoritesRemoveOptions{Component: newKey})).must(t)

	ok(c.ProjectBranches.List(ctx, &sq.ProjectBranchesListOptions{Project: newKey})).must(t)
	done(c.ProjectBranches.Rename(ctx, &sq.ProjectBranchesRenameOptions{Project: newKey, Name: "trunk"})).must(t)
	// Community Edition has a single branch: it cannot be deleted, made main
	// again, or have its protection changed.
	done(c.ProjectBranches.SetAutomaticDeletionProtection(ctx, &sq.ProjectBranchesSetAutomaticDeletionProtectionOptions{Project: newKey, Branch: "trunk", Value: sq.Ptr(true)})).allow(t, 400)
	done(c.ProjectBranches.SetMain(ctx, &sq.ProjectBranchesSetMainOptions{Project: newKey, Branch: "trunk"})).allow(t, 400)
	done(c.ProjectBranches.Delete(ctx, &sq.ProjectBranchesDeleteOptions{Project: newKey, Branch: "trunk"})).allow(t, 400)

	ok(c.ProjectBadges.Token(ctx, &sq.ProjectBadgesTokenOptions{Project: newKey})).must(t)
	done(c.ProjectBadges.RenewToken(ctx, &sq.ProjectBadgesRenewTokenOptions{Project: newKey})).must(t)
	ok(c.ProjectBadges.Measure(ctx, &sq.ProjectBadgesMeasureOptions{Project: scanned, Metric: "bugs"})).must(t)
	ok(c.ProjectBadges.QualityGate(ctx, &sq.ProjectBadgesQualityGateOptions{Project: scanned})).must(t)

	ok(c.NewCodePeriods.Show(ctx, &sq.NewCodePeriodsShowOptions{Project: newKey})).must(t)
	done(c.NewCodePeriods.Set(ctx, &sq.NewCodePeriodsSetOptions{Project: newKey, Type: "NUMBER_OF_DAYS", Value: "30"})).must(t)
	ok(c.NewCodePeriods.List(ctx, &sq.NewCodePeriodsListOptions{Project: newKey})).must(t)
	done(c.NewCodePeriods.Unset(ctx, &sq.NewCodePeriodsUnsetOptions{Project: newKey})).must(t)

	ok(c.ProjectDump.Status(ctx, &sq.ProjectDumpStatusOptions{Key: newKey})).must(t)
	ok(c.ProjectDump.Export(ctx, &sq.ProjectDumpExportOptions{Key: newKey})).allow(t, 400, 403)

	ok(c.AnalysisCache.Get(ctx, &sq.AnalysisCacheGetOptions{Project: scanned})).allow(t, 404)
	done(c.AnalysisCache.Clear(ctx, &sq.AnalysisCacheClearOptions{Project: newKey})).must(t)

	// Bulk delete the renamed project; Delete is exercised on a second one.
	done(c.Projects.BulkDelete(ctx, &sq.ProjectsBulkDeleteOptions{Projects: []string{newKey}})).must(t)
	other := uniq("it-proj-del")
	ok(c.Projects.Create(ctx, &sq.ProjectsCreateOptions{Project: other, Name: other})).must(t)
	done(c.Projects.Delete(ctx, &sq.ProjectsDeleteOptions{Project: other})).must(t)
}

func TestComponentsMeasuresSources(t *testing.T) {
	files := ok(c.Components.Tree(ctx, &sq.ComponentsTreeOptions{Component: scanned, Qualifiers: []string{"FIL"}})).must(t)
	if len(files.Components) == 0 {
		t.Fatal("scanned project has no files")
	}
	var py string
	for _, f := range files.Components {
		if strings.HasSuffix(f.Key, "app.py") {
			py = f.Key
		}
	}
	ok(c.Components.Show(ctx, &sq.ComponentsShowOptions{Component: py})).must(t)
	ok(c.Components.Search(ctx, &sq.ComponentsSearchOptions{Qualifiers: []string{"TRK"}, Q: "it-scan"})).must(t)
	ok(c.Components.SearchProjects(ctx, &sq.ComponentsSearchProjectsOptions{Filter: "ncloc > 1"})).must(t)
	ok(c.Components.Suggestions(ctx, &sq.ComponentsSuggestionsOptions{S: "it-scan"})).must(t)
	ok(c.Components.App(ctx, &sq.ComponentsAppOptions{Component: py})).must(t)

	m := ok(c.Measures.Component(ctx, &sq.MeasuresComponentOptions{Component: scanned, MetricKeys: []string{"ncloc", "bugs", "coverage", "duplicated_lines_density"}, AdditionalFields: []string{"metrics", "period"}})).must(t)
	if m.Component == nil || len(m.Component.Measures) == 0 {
		t.Fatalf("measures = %s", jsonOf(m))
	}
	ok(c.Measures.ComponentTree(ctx, &sq.MeasuresComponentTreeOptions{Component: scanned, MetricKeys: []string{"ncloc", "complexity"}, AdditionalFields: []string{"metrics", "period"}})).must(t)
	ok(c.Measures.Search(ctx, &sq.MeasuresSearchOptions{ProjectKeys: []string{scanned}, MetricKeys: []string{"ncloc", "alert_status"}})).must(t)
	ok(c.Measures.SearchHistory(ctx, &sq.MeasuresSearchHistoryOptions{Component: scanned, Metrics: []string{"ncloc", "violations"}})).must(t)

	if raw := ok(c.Sources.Raw(ctx, &sq.SourcesRawOptions{Key: py})).must(t); !strings.Contains(string(raw), "hashlib") {
		t.Errorf("raw = %.80s", raw)
	}
	ok(c.Sources.Show(ctx, &sq.SourcesShowOptions{Key: py})).must(t)
	ok(c.Sources.Lines(ctx, &sq.SourcesLinesOptions{Key: py})).must(t)
	ok(c.Sources.Index(ctx, &sq.SourcesIndexOptions{Resource: py})).must(t)
	ok(c.Sources.Scm(ctx, &sq.SourcesScmOptions{Key: py})).must(t)

	var dup string
	for _, f := range files.Components {
		if strings.HasSuffix(f.Key, "dup1.js") {
			dup = f.Key
		}
	}
	d := ok(c.Duplications.Show(ctx, &sq.DuplicationsShowOptions{Key: dup})).must(t)
	if len(d.Duplications) == 0 {
		t.Errorf("no duplications in %s", dup)
	}
}

func TestProjectAnalyses(t *testing.T) {
	as := ok(c.ProjectAnalyses.Search(ctx, &sq.ProjectAnalysesSearchOptions{Project: scanned})).must(t)
	if len(as.Analyses) < 2 {
		t.Fatalf("want 2 analyses, got %d", len(as.Analyses))
	}
	latest, older := as.Analyses[0].Key, as.Analyses[len(as.Analyses)-1].Key
	ev := ok(c.ProjectAnalyses.CreateEvent(ctx, &sq.ProjectAnalysesCreateEventOptions{Analysis: latest, Name: uniq("it-event"), Category: "OTHER"})).must(t)
	ok(c.ProjectAnalyses.UpdateEvent(ctx, &sq.ProjectAnalysesUpdateEventOptions{Event: ev.Event.Key, Name: uniq("it-event2")})).must(t)
	done(c.ProjectAnalyses.DeleteEvent(ctx, &sq.ProjectAnalysesDeleteEventOptions{Event: ev.Event.Key})).must(t)
	if len(as.Analyses) > 2 {
		done(c.ProjectAnalyses.Delete(ctx, &sq.ProjectAnalysesDeleteOptions{Analysis: older})).must(t)
	} else {
		// Keep two analyses for re-runs: deleting the last analysis is
		// rejected, which proves the route without losing data.
		done(c.ProjectAnalyses.Delete(ctx, &sq.ProjectAnalysesDeleteOptions{Analysis: latest})).allow(t, 400)
	}
}
