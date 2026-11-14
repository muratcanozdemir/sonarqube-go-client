//go:build integration

package integration

import (
	"bytes"
	"io"
	"testing"
	"time"

	sq "github.com/muratcanozdemir/sonarqube-go-client/sonarqube"
)

func TestSettings(t *testing.T) {
	proj := newProject(t, "it-settings")
	ok(c.Settings.ListDefinitions(ctx, &sq.SettingsListDefinitionsOptions{Component: proj})).must(t)
	done(c.Settings.Set(ctx, &sq.SettingsSetOptions{Key: "sonar.exclusions", Values: []string{"**/gen/**", "**/vendor/**"}, Component: proj})).must(t)
	done(c.Settings.Set(ctx, &sq.SettingsSetOptions{Key: "sonar.links.homepage", Value: "https://example.com", Component: proj})).must(t)
	vals := ok(c.Settings.Values(ctx, &sq.SettingsValuesOptions{Keys: []string{"sonar.exclusions", "sonar.links.homepage"}, Component: proj})).must(t)
	if len(vals.Settings) != 2 {
		t.Errorf("settings = %s", jsonOf(vals))
	}
	done(c.Settings.Reset(ctx, &sq.SettingsResetOptions{Keys: []string{"sonar.exclusions", "sonar.links.homepage"}, Component: proj})).must(t)
	ok(c.Settings.LoginMessage(ctx)).must(t)
	ok(c.Settings.CheckSecretKey(ctx)).must(t)
	ok(c.Settings.GenerateSecretKey(ctx)).must(t)
	// Encryption needs the generated key installed in the server's
	// sonar.secretKeyPath, which a container test cannot do.
	ok(c.Settings.Encrypt(ctx, &sq.SettingsEncryptOptions{Value: "secret"})).allow(t, 400)
}

func TestWebhooks(t *testing.T) {
	proj := newProject(t, "it-hook")
	wh := ok(c.Webhooks.Create(ctx, &sq.WebhooksCreateOptions{Name: "it", URL: "https://example.com/hook", Project: proj, Secret: "s3cr3t-s3cr3t-s3cr3t"})).must(t)
	key := wh.Webhook.Key
	ok(c.Webhooks.List(ctx, &sq.WebhooksListOptions{Project: proj})).must(t)
	done(c.Webhooks.Update(ctx, &sq.WebhooksUpdateOptions{Webhook: key, Name: "it2", URL: "https://example.com/hook2"})).must(t)
	ok(c.Webhooks.Deliveries(ctx, &sq.WebhooksDeliveriesOptions{Webhook: key})).must(t)
	// No analysis ran for this project, so there is no delivery to show.
	ok(c.Webhooks.Delivery(ctx, &sq.WebhooksDeliveryOptions{DeliveryID: "it-no-such-delivery"})).allow(t, 404)
	done(c.Webhooks.Delete(ctx, &sq.WebhooksDeleteOptions{Webhook: key})).must(t)
}

func TestComputeEngine(t *testing.T) {
	act := ok(c.Ce.Activity(ctx, &sq.CeActivityOptions{Component: scanned})).must(t)
	if len(act.Tasks) == 0 {
		t.Fatal("no CE activity for the scanned project")
	}
	ok(c.Ce.Task(ctx, &sq.CeTaskOptions{ID: act.Tasks[0].ID.String(), AdditionalFields: []string{"warnings", "scannerContext"}})).must(t)
	ok(c.Ce.ActivityStatus(ctx, nil)).must(t)
	ok(c.Ce.Component(ctx, &sq.CeComponentOptions{Component: scanned})).must(t)
	ok(c.Ce.Info(ctx)).must(t)
	ok(c.Ce.IndexationStatus(ctx)).must(t)
	ok(c.Ce.TaskTypes(ctx)).must(t)
	ok(c.Ce.WorkerCount(ctx)).must(t)
	st := ok(c.Ce.AnalysisStatus(ctx, &sq.CeAnalysisStatusOptions{Component: scanned})).must(t)
	if st.Component != nil && len(st.Component.Warnings) > 0 {
		// Scanner-generated warnings such as the Python version notice are
		// not dismissible; SonarQube answers 400 for those.
		done(c.Ce.DismissAnalysisWarning(ctx, &sq.CeDismissAnalysisWarningOptions{Component: scanned, Warning: st.Component.Warnings[0].Key})).allow(t, 400)
	} else {
		done(c.Ce.DismissAnalysisWarning(ctx, &sq.CeDismissAnalysisWarningOptions{Component: scanned, Warning: "it-no-such-warning"})).allow(t, 404)
	}

	// Pause the workers so submitted reports stay pending and can be
	// cancelled; the reports are not real analyses.
	done(c.Ce.Pause(ctx)).must(t)
	t.Cleanup(func() { _, _ = c.Ce.Resume(ctx) })
	proj := newProject(t, "it-ce")
	submit := func() string {
		r := ok(c.Ce.Submit(ctx, &sq.CeSubmitOptions{ProjectKey: proj, Report: &sq.File{Name: "report.zip", Content: emptyZip()}})).must(t)
		return r.TaskID.String()
	}
	id := submit()
	if task := ok(c.Ce.Task(ctx, &sq.CeTaskOptions{ID: id})).must(t); task.Task.Status != "PENDING" {
		t.Errorf("task status = %s", task.Task.Status)
	}
	done(c.Ce.Cancel(ctx, &sq.CeCancelOptions{ID: id})).must(t)
	submit()
	done(c.Ce.CancelAll(ctx)).must(t)
	done(c.Ce.Resume(ctx)).must(t)
}

// emptyZip returns a valid, empty zip archive.
func emptyZip() io.Reader {
	return bytes.NewReader([]byte{0x50, 0x4b, 0x05, 0x06, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
}

func TestPlugins(t *testing.T) {
	inst := ok(c.Plugins.Installed(ctx, nil)).must(t)
	if len(inst.Plugins) == 0 {
		t.Fatal("no plugins installed")
	}
	if b := ok(c.Plugins.Download(ctx, &sq.PluginsDownloadOptions{Plugin: inst.Plugins[0].Key})).must(t); len(b) < 1000 {
		t.Errorf("plugin jar is %d bytes", len(b))
	}
	ok(c.Plugins.Pending(ctx)).must(t)
	// The marketplace needs internet access to the update center; the
	// install/update/uninstall calls use a plugin that does not exist.
	ok(c.Plugins.Available(ctx)).allow(t, 503)
	ok(c.Plugins.Updates(ctx)).allow(t, 503)
	done(c.Plugins.Install(ctx, &sq.PluginsInstallOptions{Key: "it-no-such-plugin"})).allow(t, 400, 404, 503)
	done(c.Plugins.Update(ctx, &sq.PluginsUpdateOptions{Key: "it-no-such-plugin"})).allow(t, 400, 404, 503)
	done(c.Plugins.Uninstall(ctx, &sq.PluginsUninstallOptions{Key: "it-no-such-plugin"})).allow(t, 400, 404)
	done(c.Plugins.CancelAll(ctx)).must(t)
}

func TestNotifications(t *testing.T) {
	proj := newProject(t, "it-notif")
	done(c.Notifications.Add(ctx, &sq.NotificationsAddOptions{Type: "CeReportTaskFailure", Project: proj})).must(t)
	ok(c.Notifications.List(ctx, nil)).must(t)
	done(c.Notifications.Remove(ctx, &sq.NotificationsRemoveOptions{Type: "CeReportTaskFailure", Project: proj})).must(t)
	if !available("Notifications.AddGroup") {
		return
	}
	g := ok(c.Authorizations.CreateGroup(ctx, &sq.AuthorizationsCreateGroupOptions{Body: &sq.GroupCreateRestRequest{Name: uniq("it-notif-group")}})).must(t)
	t.Cleanup(func() { _, _ = c.Authorizations.DeleteGroup(ctx, &sq.AuthorizationsDeleteGroupOptions{ID: g.ID}) })
	// Community Edition offers no group notification types (the allowed
	// type list is empty), so add/remove are rejected by validation.
	done(c.Notifications.AddGroup(ctx, &sq.NotificationsAddGroupOptions{GroupUUID: g.ID, Type: "CeReportTaskFailure"})).allow(t, 400)
	ok(c.Notifications.ListGroups(ctx)).must(t)
	ok(c.Notifications.ListGroupSubscriptions(ctx)).must(t)
	done(c.Notifications.RemoveGroup(ctx, &sq.NotificationsRemoveGroupOptions{GroupUUID: g.ID, Type: "CeReportTaskFailure"})).allow(t, 400)
}

func TestHistoryAndDashboards(t *testing.T) {
	if available("History.GetMeasuresHistory") {
		id := mainBranchID(t)
		start := time.Now().AddDate(0, -1, 0).UTC().Format(time.RFC3339)
		ok(c.History.GetMeasuresHistory(ctx, &sq.HistoryGetMeasuresHistoryOptions{EntityType: "PROJECT_BRANCH", EntityID: id, MetricKeys: []string{"ncloc"}, StartDate: start})).must(t)
		ok(c.History.GetIssueCountHistory(ctx, &sq.HistoryGetIssueCountHistoryOptions{EntityType: "PROJECT_BRANCH", EntityID: id, StartDate: start})).must(t)
		// Project lists are scoped to portfolios or applications, which
		// Community Edition does not have.
		ok(c.History.GetProjectIssueCounts(ctx, nil)).allow(t, 400)
		ok(c.History.GetProjectMeasures(ctx, &sq.HistoryGetProjectMeasuresOptions{MetricKey: "ncloc"})).allow(t, 400)
		if available("History.GetIssueDensityHistory") {
			ok(c.History.GetIssueDensityHistory(ctx, &sq.HistoryGetIssueDensityHistoryOptions{EntityType: "PROJECT_BRANCH", EntityID: id, StartDate: start})).must(t)
			ok(c.History.GetIssueResolutionHistory(ctx, &sq.HistoryGetIssueResolutionHistoryOptions{EntityType: "PROJECT_BRANCH", EntityID: id, Statistic: "MTTR", StartDate: start})).must(t)
			ok(c.History.GetProjectIssueResolution(ctx, &sq.HistoryGetProjectIssueResolutionOptions{Statistic: "MTTR"})).allow(t, 400)
		}
	}
	if available("Dashboards.ListBuiltIn") {
		bi := ok(c.Dashboards.ListBuiltIn(ctx, nil)).must(t)
		if len(bi.Dashboards) > 0 {
			ok(c.Dashboards.GetBuiltIn(ctx, &sq.DashboardsGetBuiltInOptions{Key: bi.Dashboards[0].Key})).must(t)
		}
		ok(c.Dashboards.Get(ctx, &sq.DashboardsGetOptions{ID: "it-no-such-dashboard"})).allow(t, 400, 404)
	}
}

func mainBranchID(t *testing.T) string {
	t.Helper()
	for _, b := range ok(c.ProjectBranches.List(ctx, &sq.ProjectBranchesListOptions{Project: scanned})).must(t).Branches {
		if b.IsMain {
			return b.BranchID.String()
		}
	}
	t.Fatal("no main branch")
	return ""
}
