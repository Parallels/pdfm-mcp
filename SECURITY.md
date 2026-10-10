# Security Policy

## Supported versions

Security fixes are released for the latest published version only.

## Reporting a vulnerability

Please do not open a public issue for security problems.

Report privately by email to **carlos.lapao@parallels.com**, or use GitHub's
private vulnerability reporting ("Report a vulnerability" under the
repository's Security tab) if it is enabled.

Include the affected version, steps to reproduce, and the impact you see.
Do not include real credentials, tokens or customer data in the report.

You can expect an acknowledgement within a few business days. We will keep you
informed while the issue is investigated and fixed, and credit you in the
release notes if you wish.

## Scope notes

`pdfm-mcp` runs commands against local Parallels Desktop virtual machines on
behalf of an MCP client. Screenshots and OCR text it returns reach whichever
model provider your client uses. See the "Safety policy" section of the README
for `--read-only`, tool allow/deny lists and the VM allow-list.
