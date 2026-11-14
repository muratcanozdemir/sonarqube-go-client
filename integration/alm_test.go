//go:build integration

package integration

import (
	"strings"
	"testing"

	sq "github.com/muratcanozdemir/sonarqube-go-client/sonarqube"
)

// DevOps platform integrations cannot reach real GitHub/GitLab/Azure/Bitbucket
// instances from the test server. Configuration CRUD is exercised fully; calls
// that contact the platform must be rejected by SonarQube's validation (400),
// for missing resources (404), or because the remote host is unreachable.
var unreachable = []int{400, 404, 500, 503}

func TestAlmSettings(t *testing.T) {
	ok(c.AlmSettings.ListDefinitions(ctx)).must(t)
	// Community Edition allows one setting per platform: drop leftovers.
	for _, s := range ok(c.AlmSettings.List(ctx, nil)).must(t).AlmSettings {
		if strings.HasPrefix(s.Key, "it-") {
			done(c.AlmSettings.Delete(ctx, &sq.AlmSettingsDeleteOptions{Key: s.Key})).must(t)
		}
	}
	gitlab, github, azure, bbs, bbc := uniq("it-gitlab"), uniq("it-github"), uniq("it-azure"), uniq("it-bbs"), uniq("it-bbc")
	t.Cleanup(func() {
		for _, k := range []string{gitlab, github, azure, bbs, bbc} {
			_, _ = c.AlmSettings.Delete(ctx, &sq.AlmSettingsDeleteOptions{Key: k})
		}
	})
	done(c.AlmSettings.CreateGitlab(ctx, &sq.AlmSettingsCreateGitlabOptions{Key: gitlab, URL: "https://gitlab.invalid/api/v4", PersonalAccessToken: "pat"})).must(t)
	done(c.AlmSettings.CreateGithub(ctx, &sq.AlmSettingsCreateGithubOptions{Key: github, URL: "https://github.invalid/api/v3", AppID: "1", ClientID: "cid", ClientSecret: "secret", PrivateKey: "key"})).must(t)
	done(c.AlmSettings.CreateAzure(ctx, &sq.AlmSettingsCreateAzureOptions{Key: azure, URL: "https://azure.invalid", PersonalAccessToken: "pat"})).must(t)
	done(c.AlmSettings.CreateBitbucket(ctx, &sq.AlmSettingsCreateBitbucketOptions{Key: bbs, URL: "https://bitbucket.invalid", PersonalAccessToken: "pat"})).must(t)
	if l := ok(c.AlmSettings.List(ctx, nil)).must(t); len(l.AlmSettings) < 4 {
		t.Errorf("alm settings = %s", jsonOf(l))
	}
	done(c.AlmSettings.UpdateGitlab(ctx, &sq.AlmSettingsUpdateGitlabOptions{Key: gitlab, URL: "https://gitlab2.invalid/api/v4", PersonalAccessToken: "pat2"})).must(t)
	done(c.AlmSettings.UpdateGithub(ctx, &sq.AlmSettingsUpdateGithubOptions{Key: github, URL: "https://github2.invalid/api/v3", AppID: "2", ClientID: "cid2", ClientSecret: "secret2", PrivateKey: "key2"})).must(t)
	done(c.AlmSettings.UpdateAzure(ctx, &sq.AlmSettingsUpdateAzureOptions{Key: azure, URL: "https://azure2.invalid", PersonalAccessToken: "pat2"})).must(t)
	done(c.AlmSettings.UpdateBitbucket(ctx, &sq.AlmSettingsUpdateBitbucketOptions{Key: bbs, URL: "https://bitbucket2.invalid", PersonalAccessToken: "pat2"})).must(t)
	ok(c.AlmSettings.CountBinding(ctx, &sq.AlmSettingsCountBindingOptions{AlmSetting: gitlab})).must(t)
	ok(c.AlmSettings.GetBinding(ctx, &sq.AlmSettingsGetBindingOptions{Project: scanned})).allow(t, 404)
	ok(c.AlmSettings.Validate(ctx, &sq.AlmSettingsValidateOptions{Key: gitlab})).allow(t, unreachable...)
	if available("AlmSettings.CreateGithubFromManifest") {
		ok(c.AlmSettings.CreateGithubFromManifest(ctx, &sq.AlmSettingsCreateGithubFromManifestOptions{Key: uniq("it-ghm"), Name: "it", Organization: "it-org"})).allow(t, unreachable...)
	}

	// alm_integrations: a PAT can be stored; everything else calls the platform.
	done(c.AlmIntegrations.SetPat(ctx, &sq.AlmIntegrationsSetPatOptions{AlmSetting: gitlab, PAT: "pat"})).must(t)
	done(c.AlmIntegrations.CheckPat(ctx, &sq.AlmIntegrationsCheckPatOptions{AlmSetting: gitlab})).allow(t, unreachable...)
	ok(c.AlmIntegrations.GetGithubClientID(ctx, &sq.AlmIntegrationsGetGithubClientIDOptions{AlmSetting: github})).must(t)
	ok(c.AlmIntegrations.SearchGitlabRepos(ctx, &sq.AlmIntegrationsSearchGitlabReposOptions{AlmSetting: gitlab})).allow(t, unreachable...)
	done(c.AlmIntegrations.ImportGitlabProject(ctx, &sq.AlmIntegrationsImportGitlabProjectOptions{AlmSetting: gitlab, GitlabProjectID: "1"})).allow(t, unreachable...)
	ok(c.AlmIntegrations.ListGithubOrganizations(ctx, &sq.AlmIntegrationsListGithubOrganizationsOptions{AlmSetting: github, Token: "t"})).allow(t, unreachable...)
	ok(c.AlmIntegrations.ListGithubRepositories(ctx, &sq.AlmIntegrationsListGithubRepositoriesOptions{AlmSetting: github, Organization: "o"})).allow(t, unreachable...)
	done(c.AlmIntegrations.ImportGithubProject(ctx, &sq.AlmIntegrationsImportGithubProjectOptions{AlmSetting: github, RepositoryKey: "o/r"})).allow(t, unreachable...)
	ok(c.AlmIntegrations.ListAzureProjects(ctx, &sq.AlmIntegrationsListAzureProjectsOptions{AlmSetting: azure})).allow(t, unreachable...)
	ok(c.AlmIntegrations.SearchAzureRepos(ctx, &sq.AlmIntegrationsSearchAzureReposOptions{AlmSetting: azure})).allow(t, unreachable...)
	done(c.AlmIntegrations.ImportAzureProject(ctx, &sq.AlmIntegrationsImportAzureProjectOptions{AlmSetting: azure, ProjectName: "p", RepositoryName: "r"})).allow(t, unreachable...)
	ok(c.AlmIntegrations.ListBitbucketServerProjects(ctx, &sq.AlmIntegrationsListBitbucketServerProjectsOptions{AlmSetting: bbs})).allow(t, unreachable...)
	ok(c.AlmIntegrations.SearchBitbucketServerRepos(ctx, &sq.AlmIntegrationsSearchBitbucketServerReposOptions{AlmSetting: bbs, ProjectName: "p"})).allow(t, unreachable...)
	done(c.AlmIntegrations.ImportBitbucketServerProject(ctx, &sq.AlmIntegrationsImportBitbucketServerProjectOptions{AlmSetting: bbs, ProjectKey: "P", RepositorySlug: "r"})).allow(t, unreachable...)

	// Community Edition allows a single Bitbucket setting (Server or Cloud).
	done(c.AlmSettings.Delete(ctx, &sq.AlmSettingsDeleteOptions{Key: bbs})).must(t)
	done(c.AlmSettings.CreateBitbucketCloud(ctx, &sq.AlmSettingsCreateBitbucketCloudOptions{Key: bbc, ClientID: "cid", ClientSecret: "secret", Workspace: "ws"})).must(t)
	done(c.AlmSettings.UpdateBitbucketCloud(ctx, &sq.AlmSettingsUpdateBitbucketCloudOptions{Key: bbc, ClientID: "cid2", ClientSecret: "secret2", Workspace: "ws2"})).must(t)
	ok(c.AlmIntegrations.SearchBitbucketCloudRepos(ctx, &sq.AlmIntegrationsSearchBitbucketCloudReposOptions{AlmSetting: bbc})).allow(t, unreachable...)
	done(c.AlmIntegrations.ImportBitbucketCloudRepo(ctx, &sq.AlmIntegrationsImportBitbucketCloudRepoOptions{AlmSetting: bbc, RepositorySlug: "r"})).allow(t, unreachable...)

	// v2 DevOps translation layer.
	ok(c.DopTranslation.GetDopSettings(ctx)).must(t)
	bound := &sq.BoundProjectCreateRestRequest{DevOpsPlatformSettingID: gitlab, ProjectKey: uniq("it-bound"), ProjectName: "bound", RepositoryIdentifier: "1"}
	ok(c.DopTranslation.CreateBoundProject(ctx, &sq.DopTranslationCreateBoundProjectOptions{Body: bound})).allow(t, unreachable...)
	if available("DopTranslation.CreateOrUpdateBoundProject") {
		ok(c.DopTranslation.CreateOrUpdateBoundProject(ctx, &sq.DopTranslationCreateOrUpdateBoundProjectOptions{Body: bound})).allow(t, unreachable...)
	}
	ok(c.DopTranslation.SearchProjectBindings(ctx, nil)).must(t)
	ok(c.DopTranslation.GetProjectBinding(ctx, &sq.DopTranslationGetProjectBindingOptions{ID: "it-no-such-binding"})).allow(t, 404)
	if available("DopTranslation.GenerateInstallationToken") {
		ok(c.DopTranslation.GenerateInstallationToken(ctx, &sq.DopTranslationGenerateInstallationTokenOptions{Project: scanned})).allow(t, unreachable...)
	}
	if available("DopTranslation.GenerateScmAccessToken") {
		ok(c.DopTranslation.GenerateScmAccessToken(ctx, &sq.DopTranslationGenerateScmAccessTokenOptions{Project: scanned})).allow(t, unreachable...)
		ok(c.DopTranslation.CheckPermissions(ctx, &sq.DopTranslationCheckPermissionsOptions{Project: scanned})).allow(t, unreachable...)
	}
	if available("DopTranslation.GetJfrogEvidence") {
		// Enterprise-only: Community Edition answers 403 before looking up the task.
		ok(c.DopTranslation.GetJfrogEvidence(ctx, &sq.DopTranslationGetJfrogEvidenceOptions{TaskID: "it-no-such-task"})).allow(t, 403, 404)
	}
}

func TestDevOpsAuthConfigurations(t *testing.T) {
	gh := ok(c.DopTranslation.CreateGithubConfiguration(ctx, &sq.DopTranslationCreateGithubConfigurationOptions{Body: &sq.GithubConfigurationCreateRestRequest{
		APIURL: "https://api.github.invalid", WebURL: "https://github.invalid", ApplicationID: "1", ClientID: "cid", ClientSecret: "secret",
		PrivateKey: "key", ProvisioningType: "JIT", AllowedOrganizations: []string{}, Enabled: false,
	}})).must(t)
	ok(c.DopTranslation.SearchGithubConfigurations(ctx)).must(t)
	ok(c.DopTranslation.GetGithubConfiguration(ctx, &sq.DopTranslationGetGithubConfigurationOptions{ID: gh.ID})).must(t)
	ok(c.DopTranslation.UpdateGithubConfiguration(ctx, &sq.DopTranslationUpdateGithubConfigurationOptions{ID: gh.ID, Body: &sq.GithubConfigurationUpdateRestRequest{ClientID: sq.Ptr("cid2")}})).must(t)
	ok(c.GithubProvisioning.Check(ctx)).allow(t, unreachable...)
	done(c.DopTranslation.DeleteGithubConfiguration(ctx, &sq.DopTranslationDeleteGithubConfigurationOptions{ID: gh.ID})).must(t)

	gl := ok(c.DopTranslation.CreateGitlabConfiguration(ctx, &sq.DopTranslationCreateGitlabConfigurationOptions{Body: &sq.GitlabConfigurationCreateRestRequest{
		URL: "https://gitlab.invalid", ApplicationID: "1", Secret: "secret", ProvisioningType: "JIT", AllowedGroups: []string{}, Enabled: false,
	}})).must(t)
	ok(c.DopTranslation.SearchGitlabConfigurations(ctx)).must(t)
	ok(c.DopTranslation.GetGitlabConfiguration(ctx, &sq.DopTranslationGetGitlabConfigurationOptions{ID: gl.ID})).must(t)
	ok(c.DopTranslation.UpdateGitlabConfiguration(ctx, &sq.DopTranslationUpdateGitlabConfigurationOptions{ID: gl.ID, Body: &sq.GitlabConfigurationUpdateRestRequest{ApplicationID: sq.Ptr("2")}})).must(t)
	done(c.DopTranslation.DeleteGitlabConfiguration(ctx, &sq.DopTranslationDeleteGitlabConfigurationOptions{ID: gl.ID})).must(t)
}
