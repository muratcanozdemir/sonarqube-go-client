//go:build integration

package integration

import (
	"strings"
	"testing"

	sq "github.com/muratcanozdemir/sonarqube-go-client/sonarqube"
)

func TestQualityGates(t *testing.T) {
	name := uniq("it-gate")
	ok(c.Qualitygates.Create(ctx, &sq.QualitygatesCreateOptions{Name: name})).must(t)
	cond := ok(c.Qualitygates.CreateCondition(ctx, &sq.QualitygatesCreateConditionOptions{GateName: name, Metric: "coverage", Op: "LT", Error: "80"})).must(t)
	done(c.Qualitygates.UpdateCondition(ctx, &sq.QualitygatesUpdateConditionOptions{ID: cond.ID.String(), Metric: "coverage", Op: "LT", Error: "70"})).must(t)
	if g := ok(c.Qualitygates.Show(ctx, &sq.QualitygatesShowOptions{Name: name})).must(t); len(g.Conditions) == 0 {
		t.Error("condition missing")
	}
	done(c.Qualitygates.DeleteCondition(ctx, &sq.QualitygatesDeleteConditionOptions{ID: cond.ID.String()})).must(t)

	copyName := name + "-copy"
	done(c.Qualitygates.Copy(ctx, &sq.QualitygatesCopyOptions{SourceName: name, Name: copyName})).must(t)
	renamed := name + "-renamed"
	done(c.Qualitygates.Rename(ctx, &sq.QualitygatesRenameOptions{CurrentName: copyName, Name: renamed})).must(t)
	ok(c.Qualitygates.List(ctx)).must(t)

	done(c.Qualitygates.Assign(ctx, &sq.QualitygatesAssignOptions{GateName: name, ProjectKey: scanned})).must(t)
	if g := ok(c.Qualitygates.GetByProject(ctx, &sq.QualitygatesGetByProjectOptions{Project: scanned})).must(t); g.QualityGate.Name != name {
		t.Errorf("gate = %+v", g.QualityGate)
	}
	ok(c.Qualitygates.Search(ctx, &sq.QualitygatesSearchOptions{GateName: name, Selected: "all"})).must(t)
	done(c.Qualitygates.Unassign(ctx, &sq.QualitygatesUnassignOptions{ProjectKey: scanned})).must(t)
	ps := ok(c.Qualitygates.ProjectStatus(ctx, &sq.QualitygatesProjectStatusOptions{ProjectKey: scanned})).must(t)
	if ps.ProjectStatus == nil || ps.ProjectStatus.Status == "" {
		t.Errorf("project status = %s", jsonOf(ps))
	}

	user, login := newUser(t)
	_ = user
	group := newGroup(t)
	done(c.Qualitygates.AddUser(ctx, &sq.QualitygatesAddUserOptions{GateName: name, Login: login})).must(t)
	done(c.Qualitygates.AddGroup(ctx, &sq.QualitygatesAddGroupOptions{GateName: name, GroupName: group})).must(t)
	ok(c.Qualitygates.SearchUsers(ctx, &sq.QualitygatesSearchUsersOptions{GateName: name})).must(t)
	ok(c.Qualitygates.SearchGroups(ctx, &sq.QualitygatesSearchGroupsOptions{GateName: name})).must(t)
	done(c.Qualitygates.RemoveUser(ctx, &sq.QualitygatesRemoveUserOptions{GateName: name, Login: login})).must(t)
	done(c.Qualitygates.RemoveGroup(ctx, &sq.QualitygatesRemoveGroupOptions{GateName: name, GroupName: group})).must(t)

	// Make the new gate default, then restore the previous default.
	var def string
	for _, g := range ok(c.Qualitygates.List(ctx)).must(t).Qualitygates {
		if g.IsDefault {
			def = g.Name
		}
	}
	done(c.Qualitygates.SetDefault(ctx, &sq.QualitygatesSetDefaultOptions{Name: name})).must(t)
	done(c.Qualitygates.SetDefault(ctx, &sq.QualitygatesSetDefaultOptions{Name: def})).must(t)
	done(c.Qualitygates.Delete(ctx, &sq.QualitygatesDeleteOptions{Name: renamed})).must(t)
	done(c.Qualitygates.Delete(ctx, &sq.QualitygatesDeleteOptions{Name: name})).must(t)
}

func TestQualityProfiles(t *testing.T) {
	ok(c.Qualityprofiles.Exporters(ctx)).must(t)
	ok(c.Qualityprofiles.Importers(ctx)).must(t)
	all := ok(c.Qualityprofiles.Search(ctx, &sq.QualityprofilesSearchOptions{Language: "py"})).must(t)
	var builtIn, builtInName string
	for _, p := range all.Profiles {
		if p.IsBuiltIn {
			builtIn, builtInName = p.Key, p.Name
		}
	}
	name := uniq("it-profile")
	created := ok(c.Qualityprofiles.Create(ctx, &sq.QualityprofilesCreateOptions{Language: "py", Name: name})).must(t)
	key := created.Profile.Key
	ok(c.Qualityprofiles.Show(ctx, &sq.QualityprofilesShowOptions{Key: key})).must(t)
	done(c.Qualityprofiles.ChangeParent(ctx, &sq.QualityprofilesChangeParentOptions{Language: "py", QualityProfile: name, ParentQualityProfile: builtInName})).must(t)
	ok(c.Qualityprofiles.Inheritance(ctx, &sq.QualityprofilesInheritanceOptions{Language: "py", QualityProfile: name})).must(t)
	done(c.Qualityprofiles.ChangeParent(ctx, &sq.QualityprofilesChangeParentOptions{Language: "py", QualityProfile: name})).must(t)

	done(c.Qualityprofiles.ActivateRule(ctx, &sq.QualityprofilesActivateRuleOptions{Key: key, Rule: "python:S1481"})).must(t)
	done(c.Qualityprofiles.DeactivateRule(ctx, &sq.QualityprofilesDeactivateRuleOptions{Key: key, Rule: "python:S1481"})).must(t)
	done(c.Qualityprofiles.ActivateRules(ctx, &sq.QualityprofilesActivateRulesOptions{TargetKey: key, Languages: []string{"py"}, Tags: []string{"convention"}})).must(t)
	done(c.Qualityprofiles.DeactivateRules(ctx, &sq.QualityprofilesDeactivateRulesOptions{TargetKey: key, Languages: []string{"py"}, Tags: []string{"convention"}})).must(t)
	ok(c.Qualityprofiles.Changelog(ctx, &sq.QualityprofilesChangelogOptions{Language: "py", QualityProfile: name})).must(t)
	ok(c.Qualityprofiles.Compare(ctx, &sq.QualityprofilesCompareOptions{LeftKey: key, RightKey: builtIn})).must(t)

	backup := ok(c.Qualityprofiles.Backup(ctx, &sq.QualityprofilesBackupOptions{Language: "py", QualityProfile: name})).must(t)
	if !strings.Contains(string(backup), "<profile>") {
		t.Fatalf("backup = %.200s", backup)
	}
	ok(c.Qualityprofiles.Export(ctx, &sq.QualityprofilesExportOptions{Language: "py", QualityProfile: name})).must(t)
	// Restore under a different name.
	restoredName := name + "-restored"
	restored := strings.Replace(string(backup), "<name>"+name+"</name>", "<name>"+restoredName+"</name>", 1)
	done(c.Qualityprofiles.Restore(ctx, &sq.QualityprofilesRestoreOptions{Backup: &sq.File{Name: "backup.xml", Content: strings.NewReader(restored)}})).must(t)

	copied := ok(c.Qualityprofiles.Copy(ctx, &sq.QualityprofilesCopyOptions{FromKey: key, ToName: name + "-copy"})).must(t)
	done(c.Qualityprofiles.Rename(ctx, &sq.QualityprofilesRenameOptions{Key: copied.Key, Name: name + "-copy2"})).must(t)

	proj := newProject(t, "it-qp-proj")
	done(c.Qualityprofiles.AddProject(ctx, &sq.QualityprofilesAddProjectOptions{Language: "py", QualityProfile: name, Project: proj})).must(t)
	ok(c.Qualityprofiles.Projects(ctx, &sq.QualityprofilesProjectsOptions{Key: key})).must(t)
	done(c.Qualityprofiles.RemoveProject(ctx, &sq.QualityprofilesRemoveProjectOptions{Language: "py", QualityProfile: name, Project: proj})).must(t)

	_, login := newUser(t)
	group := newGroup(t)
	done(c.Qualityprofiles.AddUser(ctx, &sq.QualityprofilesAddUserOptions{Language: "py", QualityProfile: name, Login: login})).must(t)
	done(c.Qualityprofiles.AddGroup(ctx, &sq.QualityprofilesAddGroupOptions{Language: "py", QualityProfile: name, Group: group})).must(t)
	ok(c.Qualityprofiles.SearchUsers(ctx, &sq.QualityprofilesSearchUsersOptions{Language: "py", QualityProfile: name})).must(t)
	ok(c.Qualityprofiles.SearchGroups(ctx, &sq.QualityprofilesSearchGroupsOptions{Language: "py", QualityProfile: name})).must(t)
	done(c.Qualityprofiles.RemoveUser(ctx, &sq.QualityprofilesRemoveUserOptions{Language: "py", QualityProfile: name, Login: login})).must(t)
	done(c.Qualityprofiles.RemoveGroup(ctx, &sq.QualityprofilesRemoveGroupOptions{Language: "py", QualityProfile: name, Group: group})).must(t)

	// Make the new profile default, then restore the seeded default.
	var def string
	for _, p := range all.Profiles {
		if p.IsDefault {
			def = p.Name
		}
	}
	done(c.Qualityprofiles.SetDefault(ctx, &sq.QualityprofilesSetDefaultOptions{Language: "py", QualityProfile: name})).must(t)
	done(c.Qualityprofiles.SetDefault(ctx, &sq.QualityprofilesSetDefaultOptions{Language: "py", QualityProfile: def})).must(t)
	for _, n := range []string{name + "-copy2", restoredName, name} {
		done(c.Qualityprofiles.Delete(ctx, &sq.QualityprofilesDeleteOptions{Language: "py", QualityProfile: n})).must(t)
	}
}
