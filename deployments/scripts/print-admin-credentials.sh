#!/bin/bash

# Prints the AMP console admin login. The password is never operator-supplied
# (see wso2-amp-thunder-extension/templates/admin-credentials.yaml) — it's
# generated once by that chart and stored in the amp-admin-credentials Secret,
# so this just reads it back for display at the end of setup.
#
# Usage: print-admin-credentials.sh <console-url> [thunder-namespace]

set -euo pipefail

CONSOLE_URL="${1:?Usage: print-admin-credentials.sh <console-url> [thunder-namespace]}"
THUNDER_NS="${2:-amp-thunder}"

username="$(kubectl get secret amp-admin-credentials -n "${THUNDER_NS}" -o jsonpath='{.data.username}' 2>/dev/null | base64 -d || true)"
password="$(kubectl get secret amp-admin-credentials -n "${THUNDER_NS}" -o jsonpath='{.data.password}' 2>/dev/null | base64 -d || true)"

if [ -z "${password}" ]; then
    echo "⚠️  Could not read amp-admin-credentials in namespace ${THUNDER_NS} — Thunder may not be installed yet." >&2
    exit 1
fi

echo ""
echo "🔐 AMP Console Admin Login"
echo "   Console:  ${CONSOLE_URL}"
echo "   Username: ${username:-admin}"
echo "   Password: ${password}"
echo ""
echo "   Retrieve this again any time with:"
echo "   kubectl get secret amp-admin-credentials -n ${THUNDER_NS} -o jsonpath='{.data.password}' | base64 -d"
