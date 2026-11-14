//go:build integration

package integration

import (
	"strings"
	"testing"

	sq "github.com/muratcanozdemir/sonarqube-go-client/sonarqube"
)

func TestServerAndSystem(t *testing.T) {
	v := ok(c.Server.Version(ctx)).must(t)
	if !strings.Contains(release, strings.Join(strings.Split(string(v), ".")[:2], ".")) {
		t.Errorf("version %s does not match release %s", v, release)
	}
	if st := ok(c.System.Status(ctx)).must(t); st.Status != "UP" {
		t.Errorf("status = %+v", st)
	}
	if h := ok(c.System.Health(ctx)).must(t); h.Health == "" {
		t.Errorf("health = %+v", h)
	}
	if p := ok(c.System.Ping(ctx)).must(t); string(p) != "pong" {
		t.Errorf("ping = %q", p)
	}
	if info := ok(c.System.Info(ctx)).must(t); len(info) == 0 {
		t.Error("empty system info")
	}
	ok(c.System.DbMigrationStatus(ctx)).must(t)
	ok(c.System.MigrateDb(ctx)).must(t)
	ok(c.System.Upgrades(ctx)).allow(t, 503) // needs update center access
	if logs := ok(c.System.Logs(ctx, &sq.SystemLogsOptions{Name: "app"})).must(t); len(logs) == 0 {
		t.Error("empty logs")
	}
	done(c.System.ChangeLogLevel(ctx, &sq.SystemChangeLogLevelOptions{Level: "INFO"})).must(t)
	ok(c.SystemV2.GetHealth(ctx)).must(t)
	ok(c.SystemV2.GetMigrationsStatus(ctx)).must(t)
	// Unlike api/system/liveness, the v2 probe ignores the passcode and wants
	// an administrator.
	done(c.SystemV2.CheckLiveness(ctx)).must(t)

	// Liveness and metrics are authorised by the system passcode.
	pc := newClient(sq.WithSystemPasscode(passcode))
	done(pc.System.Liveness(ctx)).must(t)
	if m := ok(pc.Monitoring.Metrics(ctx)).must(t); !strings.Contains(string(m), "sonarqube") {
		t.Errorf("metrics = %.100s", m)
	}
	ok(c.Webservices.List(ctx, &sq.WebservicesListOptions{IncludeInternals: sq.Ptr(true)})).must(t)
	ok(c.Webservices.ResponseExample(ctx, &sq.WebservicesResponseExampleOptions{Controller: "api/issues", Action: "search"})).must(t)
	ok(c.Languages.List(ctx, nil)).must(t)
	ok(c.L10N.GetIndex(ctx, nil)).must(t)
	ok(c.Features.List(ctx)).must(t)
	ok(c.Metrics.Search(ctx, nil)).must(t)
	ok(c.Metrics.Types(ctx)).must(t)
	ok(c.AnalysisReports.QueueStatus(ctx)).must(t)
}

func TestSystemV2EmailConfigurations(t *testing.T) {
	list := ok(c.SystemV2.SearchEmailConfigurations(ctx)).must(t)
	id := ""
	if len(list.EmailConfigurations) > 0 {
		id = list.EmailConfigurations[0].ID
	} else {
		created := ok(c.SystemV2.CreateEmailConfiguration(ctx, &sq.SystemV2CreateEmailConfigurationOptions{Body: &sq.EmailConfigurationCreateRestRequest{
			Host: "smtp.example.com", Port: "25", SecurityProtocol: "NONE", FromAddress: "sonar@example.com", FromName: "Sonar",
			SubjectPrefix: "[Sonar]", AuthMethod: "BASIC", Username: "u", BasicPassword: sq.Ptr("p"),
		}})).must(t)
		id = created.ID
	}
	ok(c.SystemV2.GetEmailConfiguration(ctx, &sq.SystemV2GetEmailConfigurationOptions{ID: id})).must(t)
	ok(c.SystemV2.UpdateEmailConfiguration(ctx, &sq.SystemV2UpdateEmailConfigurationOptions{ID: id, Body: &sq.EmailConfigurationUpdateRestRequest{FromName: sq.Ptr("Sonar IT")}})).must(t)
	// Sending fails without a reachable SMTP server.
	done(c.Emails.Send(ctx, &sq.EmailsSendOptions{To: "nobody@example.com", Message: "hi"})).allow(t, 400, 500)
	done(c.SystemV2.DeleteEmailConfiguration(ctx, &sq.SystemV2DeleteEmailConfigurationOptions{ID: id})).must(t)
}

func TestNavigationAndMisc(t *testing.T) {
	ok(c.Navigation.Global(ctx)).must(t)
	ok(c.Navigation.Settings(ctx)).must(t)
	ok(c.Navigation.Marketplace(ctx)).must(t)
	ok(c.Navigation.Component(ctx, &sq.NavigationComponentOptions{Component: scanned})).must(t)
	done(c.Push.SonarlintEvents(ctx, &sq.PushSonarlintEventsOptions{ProjectKeys: []string{"it-none"}, Languages: []string{"py"}})).allow(t, 400)
	ok(c.Developers.SearchEvents(ctx, &sq.DevelopersSearchEventsOptions{Projects: []string{scanned}, From: []string{"2020-01-01T00:00:00+0000"}})).must(t)
	ok(c.DismissMessage.Check(ctx, &sq.DismissMessageCheckOptions{MessageType: "PROJECT_NCD_90", ProjectKey: scanned})).must(t)
	done(c.DismissMessage.Dismiss(ctx, &sq.DismissMessageDismissOptions{MessageType: "PROJECT_NCD_90", ProjectKey: scanned})).must(t)
	ok(c.Analysis.GetVersion(ctx)).must(t)
	ok(c.Analysis.GetActiveRules(ctx, &sq.AnalysisGetActiveRulesOptions{ProjectKey: scanned})).must(t)
	ok(c.Analysis.GetScannerEngineMetadata(ctx)).must(t)
	if b := ok(c.Analysis.DownloadScannerEngine(ctx)).must(t); len(b) < 1000 {
		t.Errorf("scanner engine is %d bytes", len(b))
	}
	jres := ok(c.Analysis.GetJresMetadata(ctx, nil)).must(t)
	if len(jres) == 0 {
		t.Fatal("no JREs")
	}
	ok(c.Analysis.GetJreMetadata(ctx, &sq.AnalysisGetJreMetadataOptions{ID: jres[0].ID})).must(t)
	// Downloading a JRE is ~50MB; a HEAD-like check is not possible, so do it once.
	ok(c.Analysis.DownloadJre(ctx, &sq.AnalysisDownloadJreOptions{ID: jres[0].ID})).must(t)
	if available("Marketplace.BillAzureAccount") {
		ok(c.Marketplace.BillAzureAccount(ctx)).allow(t, 400, 403, 404, 500)
	}
	if available("Onboarding.GetOverview") {
		ok(c.Onboarding.GetOverview(ctx, nil)).must(t)
		ok(c.Onboarding.GetProjects(ctx, nil)).must(t)
		ok(c.Onboarding.GetStatistics(ctx, nil)).must(t)
	}
	if available("Agentic.SearchJobs") {
		ok(c.Agentic.SearchJobs(ctx, nil)).allow(t, 400, 403, 404)
	}
}

// Batch endpoints serve the scanner. The matrix lists them under /api/batch,
// which SonarQube does not route; the client uses /batch.
func TestBatch(t *testing.T) {
	idx := ok(c.Batch.GetIndex(ctx)).must(t)
	if !strings.Contains(string(idx), ".jar") {
		t.Fatalf("index = %.200s", idx)
	}
	name := strings.Split(strings.Split(string(idx), "\n")[0], "|")[0]
	if b := ok(c.Batch.GetFile(ctx, &sq.BatchGetFileOptions{Name: name})).must(t); len(b) == 0 {
		t.Error("empty batch file")
	}
	ok(c.Batch.GetProject(ctx, &sq.BatchGetProjectOptions{Key: scanned})).must(t)
}
