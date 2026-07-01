package k8s

import (
	"strings"
)

// Permission is one allowlisted operation: a verb on a resource kind in a
// namespace. Empty or "*" in any field means "any". `Resource` is matched
// case-insensitively against the call's kind (e.g. "pod" matches "Pod").
type Permission struct {
	Resource  string `json:"resource,omitempty"`
	Verb      string `json:"verb,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type Settings struct {
	Kubeconfig      string       `json:"kubeconfig,omitempty"`
	Context         string       `json:"context,omitempty"`
	Permissions     []Permission `json:"permissions"`
	RequireApproval *bool        `json:"require_approval,omitempty"`
}

// knownVerbs are the operations a k8s tool can expose, in published order.
var knownVerbs = []string{"get", "list", "apply", "delete", "logs", "events"}

func isMutatingVerb(verb string) bool {
	return verb == "apply" || verb == "delete"
}

// requiresApproval reports whether a verb needs human approval. An explicit
// per-tool override wins; otherwise mutating verbs require approval.
func requiresApproval(verb string, override *bool) bool {
	if override != nil {
		return *override
	}
	return isMutatingVerb(verb)
}

type permissionPolicy struct {
	perms []Permission
}

func newPermissionPolicy(perms []Permission) permissionPolicy {
	return permissionPolicy{perms: perms}
}

// allows reports whether any permission grants this (verb, kind, namespace).
func (p permissionPolicy) allows(verb, kind, namespace string) bool {
	for _, perm := range p.perms {
		if matchToken(perm.Verb, verb) && matchToken(perm.Resource, kind) && matchToken(perm.Namespace, namespace) {
			return true
		}
	}
	return false
}

// permittedVerbs returns the known verbs this policy grants, in published order.
// A wildcard (empty or "*") verb grants all known verbs.
func (p permissionPolicy) permittedVerbs() []string {
	wildcard := false
	granted := make(map[string]bool, len(p.perms))
	for _, perm := range p.perms {
		v := strings.ToLower(strings.TrimSpace(perm.Verb))
		if v == "" || v == "*" {
			wildcard = true
			break
		}
		granted[v] = true
	}
	out := make([]string, 0, len(knownVerbs))
	for _, v := range knownVerbs {
		if wildcard || granted[v] {
			out = append(out, v)
		}
	}
	return out
}

// matchToken matches an allowlist token against an actual value. Empty or "*"
// matches anything; a trailing "*" is a case-insensitive prefix glob; otherwise
// the comparison is exact and case-insensitive.
func matchToken(allowed, actual string) bool {
	allowed = strings.TrimSpace(allowed)
	if allowed == "" || allowed == "*" {
		return true
	}
	if prefix, ok := strings.CutSuffix(allowed, "*"); ok {
		return strings.HasPrefix(strings.ToLower(actual), strings.ToLower(prefix))
	}
	return strings.EqualFold(allowed, actual)
}
