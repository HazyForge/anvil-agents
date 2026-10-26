package subjectrouting

import "regexp"

var privateKey = regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z ]* )?PRIVATE KEY-----.*?(?:-----END (?:[A-Z ]* )?PRIVATE KEY-----|$)`)
var authorizationHeader = regexp.MustCompile(`(?im)\b(?:proxy-)?authorization\s*:\s*[^\r\n]+`)
var bearer = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
var jwt = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
var prefixedKey = regexp.MustCompile(`\b(?:sk-(?:proj-|ant-)?[A-Za-z0-9_-]{12,}|gh[pousr]_[A-Za-z0-9_]{12,}|github_pat_[A-Za-z0-9_]{12,}|xox[baprs]-[A-Za-z0-9-]{12,}|AKIA[A-Z0-9]{16})\b`)
var credentialAssignment = regexp.MustCompile(`(?i)(["']?(?:[a-z0-9_]*(?:api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|password|passwd|secret[_-]?key|private[_-]?key|token)|authorization)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}&]+)`)
var credentialURL = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^\s/@:]+:[^\s/@]+@`)

// Redact removes recognizable credential shapes before routing state leaves the
// server or is included in a live routing trace. It is a precaution, not a claim
// that arbitrary free text can be proven secret-free; callers must never add
// environment values, auth headers, or tool output to the classifier's state.
func Redact(value string) string {
	value = privateKey.ReplaceAllString(value, "[redacted private key]")
	value = authorizationHeader.ReplaceAllString(value, "Authorization: [redacted]")
	value = bearer.ReplaceAllString(value, "Bearer [redacted]")
	value = jwt.ReplaceAllString(value, "[redacted token]")
	value = prefixedKey.ReplaceAllString(value, "[redacted key]")
	value = credentialAssignment.ReplaceAllString(value, "${1}[redacted]")
	return credentialURL.ReplaceAllString(value, "${1}[redacted]@")
}
