//go:build integration

package integration

import (
	"strconv"
	"testing"

	sq "github.com/muratcanozdemir/sonarqube-go-client/sonarqube"
)

var userSeq int

// newUser creates a local user (deactivated after the test).
func newUser(t *testing.T) (*sq.UsersCreateResponse, string) {
	t.Helper()
	userSeq++
	login := uniq("it-user") + "-" + strconv.Itoa(userSeq)
	u := ok(c.Users.Create(ctx, &sq.UsersCreateOptions{Login: login, Name: "IT " + login, Password: "It-User-Passw0rd!", Email: login + "@example.com"})).must(t)
	t.Cleanup(func() { _, _, _ = c.Users.Deactivate(ctx, &sq.UsersDeactivateOptions{Login: login}) })
	return u, login
}

var groupSeq int

// newGroup creates a group (deleted after the test).
func newGroup(t *testing.T) string {
	t.Helper()
	groupSeq++
	name := uniq("it-group") + "-" + strconv.Itoa(groupSeq)
	ok(c.UserGroups.Create(ctx, &sq.UserGroupsCreateOptions{Name: name, Description: "integration"})).must(t)
	t.Cleanup(func() { _, _ = c.UserGroups.Delete(ctx, &sq.UserGroupsDeleteOptions{Name: name}) })
	return name
}

func TestUsersV1(t *testing.T) {
	_, login := newUser(t)
	ok(c.Users.Search(ctx, &sq.UsersSearchOptions{Q: login})).must(t)
	ok(c.Users.Update(ctx, &sq.UsersUpdateOptions{Login: login, Name: "Renamed"})).must(t)
	newLogin := login + "-x"
	done(c.Users.UpdateLogin(ctx, &sq.UsersUpdateLoginOptions{Login: login, NewLogin: newLogin})).must(t)
	t.Cleanup(func() { _, _, _ = c.Users.Deactivate(ctx, &sq.UsersDeactivateOptions{Login: newLogin}) })
	done(c.Users.ChangePassword(ctx, &sq.UsersChangePasswordOptions{Login: newLogin, Password: "It-User-Passw0rd-2!"})).must(t)
	ok(c.Users.Groups(ctx, &sq.UsersGroupsOptions{Login: newLogin})).must(t)
	ok(c.Users.IdentityProviders(ctx)).must(t)
	// No external identity provider is configured on the test server.
	done(c.Users.UpdateIdentityProvider(ctx, &sq.UsersUpdateIdentityProviderOptions{Login: newLogin, NewExternalProvider: "github", NewExternalIdentity: newLogin})).allow(t, 400)

	cur := ok(c.Users.Current(ctx)).must(t)
	if cur.Login != "admin" {
		t.Errorf("current = %s", cur.Login)
	}
	done(c.Users.SetHomepage(ctx, &sq.UsersSetHomepageOptions{Type: "PROJECTS"})).must(t)
	done(c.Users.DismissNotice(ctx, &sq.UsersDismissNoticeOptions{Notice: "educationPrinciples"})).must(t)

	// Tokens for another user.
	tok := ok(c.UserTokens.Generate(ctx, &sq.UserTokensGenerateOptions{Login: newLogin, Name: "it"})).must(t)
	if tok.Token == "" {
		t.Fatal("no token")
	}
	userClient := newClient(sq.WithToken(tok.Token))
	v := ok(userClient.Authentication.Validate(ctx)).must(t)
	if !v.Valid {
		t.Error("token not valid")
	}
	ok(c.UserTokens.Search(ctx, &sq.UserTokensSearchOptions{Login: newLogin})).must(t)
	done(c.UserTokens.Revoke(ctx, &sq.UserTokensRevokeOptions{Login: newLogin, Name: "it"})).must(t)

	ok(c.Users.Deactivate(ctx, &sq.UsersDeactivateOptions{Login: newLogin})).must(t)
	done(c.Users.Anonymize(ctx, &sq.UsersAnonymizeOptions{Login: newLogin})).must(t)
}

func TestAuthentication(t *testing.T) {
	basic := newClient(sq.WithBasicAuth("admin", adminPwd))
	if v := ok(basic.Authentication.Validate(ctx)).must(t); !v.Valid {
		t.Error("basic auth not valid")
	}
	anon := newClient()
	done(anon.Authentication.Login(ctx, &sq.AuthenticationLoginOptions{Login: "admin", Password: adminPwd})).must(t)
	done(anon.Authentication.Logout(ctx)).allow(t, 401)
}

func TestUserGroups(t *testing.T) {
	name := newGroup(t)
	_, login := newUser(t)
	done(c.UserGroups.AddUser(ctx, &sq.UserGroupsAddUserOptions{Name: name, Login: login})).must(t)
	ok(c.UserGroups.Users(ctx, &sq.UserGroupsUsersOptions{Name: name})).must(t)
	done(c.UserGroups.RemoveUser(ctx, &sq.UserGroupsRemoveUserOptions{Name: name, Login: login})).must(t)
	ok(c.UserGroups.Search(ctx, &sq.UserGroupsSearchOptions{Q: name})).must(t)
	renamed := name + "-r"
	done(c.UserGroups.Update(ctx, &sq.UserGroupsUpdateOptions{CurrentName: name, Name: renamed})).must(t)
	done(c.UserGroups.Delete(ctx, &sq.UserGroupsDeleteOptions{Name: renamed})).must(t)
}

func TestUsersAndGroupsV2(t *testing.T) {
	login := uniq("it-v2user")
	u := ok(c.UsersManagement.Create(ctx, &sq.UsersManagementCreateOptions{Body: &sq.UserCreateRestRequest{
		Login: login, Name: "IT v2", Email: sq.Ptr(login + "@example.com"), Password: sq.Ptr("It-User-Passw0rd!"),
	}})).must(t)
	ok(c.UsersManagement.Get(ctx, &sq.UsersManagementGetOptions{ID: u.ID})).must(t)
	// SCM accounts are an UpdateField, which SonarQube reads as a bare list.
	upd := ok(c.UsersManagement.Update(ctx, &sq.UsersManagementUpdateOptions{ID: u.ID, Body: &sq.UserUpdateRestRequest{
		Name: sq.Ptr("IT v2 renamed"), SCMAccounts: &sq.UpdateFieldListString{Value: []string{login + "-scm"}}}})).must(t)
	if len(upd.SCMAccounts) != 1 || upd.SCMAccounts[0] != login+"-scm" {
		t.Errorf("scmAccounts after update: %v", upd.SCMAccounts)
	}
	ok(c.UsersManagement.Search(ctx, &sq.UsersManagementSearchOptions{Q: login})).must(t)

	g := ok(c.Authorizations.CreateGroup(ctx, &sq.AuthorizationsCreateGroupOptions{Body: &sq.GroupCreateRestRequest{Name: uniq("it-v2group"), Description: sq.Ptr("it")}})).must(t)
	ok(c.Authorizations.GetGroup(ctx, &sq.AuthorizationsGetGroupOptions{ID: g.ID})).must(t)
	ok(c.Authorizations.UpdateGroup(ctx, &sq.AuthorizationsUpdateGroupOptions{ID: g.ID, Body: &sq.GroupUpdateRestRequest{Description: sq.Ptr("it2")}})).must(t)
	ok(c.Authorizations.SearchGroups(ctx, &sq.AuthorizationsSearchGroupsOptions{Q: "it-v2group"})).must(t)
	m := ok(c.Authorizations.CreateGroupMembership(ctx, &sq.AuthorizationsCreateGroupMembershipOptions{Body: &sq.GroupMembershipCreateRestRequest{GroupID: sq.Ptr(g.ID), UserID: sq.Ptr(u.ID)}})).must(t)
	ok(c.Authorizations.SearchGroupMemberships(ctx, &sq.AuthorizationsSearchGroupMembershipsOptions{GroupID: g.ID})).must(t)
	done(c.Authorizations.DeleteGroupMembership(ctx, &sq.AuthorizationsDeleteGroupMembershipOptions{ID: m.ID})).must(t)
	done(c.Authorizations.DeleteGroup(ctx, &sq.AuthorizationsDeleteGroupOptions{ID: g.ID})).must(t)
	done(c.UsersManagement.Deactivate(ctx, &sq.UsersManagementDeactivateOptions{ID: u.ID, Anonymize: sq.Ptr(true)})).must(t)
}

func TestPermissions(t *testing.T) {
	proj := newProject(t, "it-perm-proj")
	_, login := newUser(t)
	group := newGroup(t)
	done(c.Permissions.AddUser(ctx, &sq.PermissionsAddUserOptions{Login: login, Permission: "user", ProjectKey: proj})).must(t)
	done(c.Permissions.AddGroup(ctx, &sq.PermissionsAddGroupOptions{GroupName: group, Permission: "codeviewer", ProjectKey: proj})).must(t)
	ok(c.Permissions.Users(ctx, &sq.PermissionsUsersOptions{ProjectKey: proj})).must(t)
	ok(c.Permissions.Groups(ctx, &sq.PermissionsGroupsOptions{ProjectKey: proj})).must(t)
	done(c.Permissions.RemoveUser(ctx, &sq.PermissionsRemoveUserOptions{Login: login, Permission: "user", ProjectKey: proj})).must(t)
	done(c.Permissions.RemoveGroup(ctx, &sq.PermissionsRemoveGroupOptions{GroupName: group, Permission: "codeviewer", ProjectKey: proj})).must(t)

	tplName := uniq("it-template")
	tpl := ok(c.Permissions.CreateTemplate(ctx, &sq.PermissionsCreateTemplateOptions{Name: tplName, ProjectKeyPattern: "it-.*"})).must(t)
	id := tpl.PermissionTemplate.ID.String()
	ok(c.Permissions.UpdateTemplate(ctx, &sq.PermissionsUpdateTemplateOptions{ID: id, Description: "integration"})).must(t)
	done(c.Permissions.AddUserToTemplate(ctx, &sq.PermissionsAddUserToTemplateOptions{TemplateID: id, Login: login, Permission: "user"})).must(t)
	done(c.Permissions.AddGroupToTemplate(ctx, &sq.PermissionsAddGroupToTemplateOptions{TemplateID: id, GroupName: group, Permission: "user"})).must(t)
	done(c.Permissions.AddProjectCreatorToTemplate(ctx, &sq.PermissionsAddProjectCreatorToTemplateOptions{TemplateID: id, Permission: "admin"})).must(t)
	ok(c.Permissions.TemplateUsers(ctx, &sq.PermissionsTemplateUsersOptions{TemplateID: id})).must(t)
	ok(c.Permissions.TemplateGroups(ctx, &sq.PermissionsTemplateGroupsOptions{TemplateID: id})).must(t)
	ok(c.Permissions.SearchTemplates(ctx, &sq.PermissionsSearchTemplatesOptions{Q: "it-template"})).must(t)
	done(c.Permissions.ApplyTemplate(ctx, &sq.PermissionsApplyTemplateOptions{TemplateID: id, ProjectKey: proj})).must(t)
	done(c.Permissions.BulkApplyTemplate(ctx, &sq.PermissionsBulkApplyTemplateOptions{TemplateID: id, Projects: []string{proj}})).must(t)
	done(c.Permissions.RemoveProjectCreatorFromTemplate(ctx, &sq.PermissionsRemoveProjectCreatorFromTemplateOptions{TemplateID: id, Permission: "admin"})).must(t)
	done(c.Permissions.RemoveUserFromTemplate(ctx, &sq.PermissionsRemoveUserFromTemplateOptions{TemplateID: id, Login: login, Permission: "user"})).must(t)
	done(c.Permissions.RemoveGroupFromTemplate(ctx, &sq.PermissionsRemoveGroupFromTemplateOptions{TemplateID: id, GroupName: group, Permission: "user"})).must(t)

	// Swap the default template and back.
	var def string
	for _, d := range ok(c.Permissions.SearchTemplates(ctx, nil)).must(t).DefaultTemplates {
		if d.Qualifier == "TRK" {
			def = d.TemplateID.String()
		}
	}
	done(c.Permissions.SetDefaultTemplate(ctx, &sq.PermissionsSetDefaultTemplateOptions{TemplateID: id})).must(t)
	done(c.Permissions.SetDefaultTemplate(ctx, &sq.PermissionsSetDefaultTemplateOptions{TemplateID: def})).must(t)
	done(c.Permissions.DeleteTemplate(ctx, &sq.PermissionsDeleteTemplateOptions{TemplateID: id})).must(t)
}
