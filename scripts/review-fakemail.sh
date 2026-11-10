#!/usr/bin/env bash
# Start the D36 fake mail server for founder local review of the email
# feature: fixed loopback ports, two review accounts and -deliver, so mail
# sent from Omnipus also lands in the recipient's INBOX (and everything is
# still recorded in the sink). Ctrl-C stops it.
#
# Omnipus allows plaintext IMAP/SMTP only to loopback; every address here is
# 127.0.0.1. Seed a sample inbound message any time with:
#   curl -s http://127.0.0.1:1180/inject \
#     -d '{"user":"agent@test.local","subject":"Welcome","text":"Hello agent."}'
set -euo pipefail

echo "================================================================"
echo " Mailbox settings to enter in Omnipus (Mail -> mailbox setup):"
echo "   IMAP host:  127.0.0.1       IMAP port:  1143"
echo "   SMTP host:  127.0.0.1       SMTP port:  1025"
echo "   Security:   none / plaintext (allowed on loopback only)"
echo "   Username:   agent@test.local"
echo "   Password:   s3cret"
echo "   Second account (to receive mail agent@test.local sends):"
echo "               alice@test.local / s3cret"
echo "   Folder set: INBOX, Sent, Drafts, Trash"
echo "================================================================"

cd "$(dirname "$0")/../tests/e2e/fixtures/fakemail"
exec env CGO_ENABLED=0 go run . \
  -imap 127.0.0.1:1143 \
  -smtp 127.0.0.1:1025 \
  -control 127.0.0.1:1180 \
  -users 'agent@test.local:s3cret,alice@test.local:s3cret' \
  -deliver
