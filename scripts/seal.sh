#!/usr/bin/env bash
#
# Encrypt an overlay's secrets into SealedSecrets that are safe to commit.
#
#	make seal OVERLAY=prod
#
# What it produces, one per service that owns a database, plus one for the
# tunnel where the overlay runs one:
#
#	deploy/k8s/overlays/<overlay>/<svc>/sealedsecret.yaml
#	deploy/k8s/overlays/<overlay>/infra/sealedsecret.yaml
#
# The overlay lists that file as a resource, the controller in the cluster
# decrypts it into the plain Secret the workload mounts, and nothing readable
# ever reaches git.
#
# Three things about it are easy to get wrong, and each fails in its own way:
#
#   the cluster    A SealedSecret is encrypted against the sealing key of one
#                  cluster, and only that cluster can read it. This seals
#                  against whatever kubectl points at right now. Sealing on a
#                  laptop and applying to a server produces a resource the
#                  controller there rejects, having no key that fits.
#
#   the namespace  Encryption is scoped to namespace and name together, so a
#                  SealedSecret cannot be copied to another namespace or renamed
#                  after the fact. Both are baked in below.
#
#   the plaintext  The material under overlays/<overlay>/ is gitignored and is
#                  the only copy. It is what re-sealing needs after a controller
#                  is reinstalled with a new key — losing it means rotating
#                  every credential rather than re-encrypting them.
set -euo pipefail

OVERLAY="${OVERLAY:-prod}"
NAMESPACE="${NAMESPACE:-ecommerce}"
K8S_DIR="${K8S_DIR:-deploy/k8s}"

# Where the signer's private key lives. Same directory `make keys` writes into,
# and identity is the only service that holds one — everything else verifies.
SIGNER=identity

overlay_dir="${K8S_DIR}/overlays/${OVERLAY}"
[[ -d "$overlay_dir" ]] || { echo "no such overlay: ${OVERLAY}" >&2; exit 1; }

info() { printf '  %s\n' "$*"; }
wrote() { printf '\033[32m  wrote\033[0m %s\n' "$*"; }

# seal_secret NAME OUT [kubectl-create-secret args...]
#
# --dry-run=client, so the readable Secret is a stream between two processes and
# never an object in the cluster or a file on disk.
seal_secret() {
    local name="$1" out="$2"
    shift 2

    kubectl create secret generic "$name" \
        --namespace "$NAMESPACE" \
        --dry-run=client -o yaml \
        "$@" |
        kubeseal --format yaml \
            --controller-namespace kube-system \
            --controller-name sealed-secrets-controller \
            >"$out"

    wrote "$out"
}

# The controller has to be reachable, because kubeseal fetches its public
# certificate before it can encrypt anything. Checking here turns a wall of Go
# error into one line naming the fix.
if ! kubectl -n kube-system get deployment sealed-secrets-controller >/dev/null 2>&1; then
    echo "sealed-secrets-controller is not installed in this cluster." >&2
    echo "It holds the only key that can decrypt what this writes — run: make up" >&2
    exit 1
fi

printf 'sealing %s into namespace %s\n' "$OVERLAY" "$NAMESPACE"
printf '        cluster %s\n\n' "$(kubectl config current-context)"

for dir in "$overlay_dir"/*/; do
    svc="$(basename "$dir")"

    # A service owns a database if it ships migrations, which is the same rule
    # the Makefile uses to decide whether to build a migrate image and run a
    # migration Job. A service without one holds no credential to seal.
    [[ -d "services/${svc}/db/migrations" ]] || continue

    prefix="$(tr '[:lower:]-' '[:upper:]_' <<<"$svc")"
    password_file="${dir}db-password"

    # Generated once and kept, never regenerated silently. Postgres reads
    # POSTGRES_PASSWORD only while initialising an empty data directory, so a
    # new password here would leave the database on the old one and the service
    # unable to log in — with both halves insisting they agree.
    if [[ ! -f "$password_file" ]]; then
        openssl rand -base64 24 | tr -d '\n' >"$password_file"
        chmod 600 "$password_file"
        wrote "$password_file"
    else
        info "$password_file already exists — delete it to rotate"
    fi

    args=(--from-literal="${prefix}_DB_PASSWORD=$(<"$password_file")")

    if [[ "$svc" == "$SIGNER" ]]; then
        key="${dir}jwt-private.pem"
        [[ -f "$key" ]] || {
            echo "${key} is missing — run: make keys OVERLAY=${OVERLAY}" >&2
            exit 1
        }
        args+=(--from-file="${prefix}_JWT_PRIVATE_KEY=${key}")
    fi

    seal_secret "${svc}-secret" "${dir}sealedsecret.yaml" "${args[@]}"
done

# The tunnel's credentials, for an overlay whose cluster is reachable from the
# internet. They are not generated here the way a database password is: the
# file is written by `cloudflared tunnel create`, which is what registers the
# tunnel with Cloudflare in the first place, and there is nothing this script
# could invent that the edge would recognise.
infra_dir="${overlay_dir}/infra"
credentials="${infra_dir}/cloudflared-credentials.json"

if [[ -f "$credentials" ]]; then
    seal_secret cloudflared-credentials "${infra_dir}/sealedsecret.yaml" \
        --from-file="credentials.json=${credentials}"
elif grep -q 'components/cloudflared' "${infra_dir}/kustomization.yaml" 2>/dev/null; then
    # The overlay runs a tunnel and the credentials for it are missing, which
    # would otherwise surface as `make up OVERLAY=${OVERLAY}` failing on a
    # sealedsecret.yaml nobody wrote.
    echo "${credentials} is missing, and this overlay includes the cloudflared component." >&2
    echo "Create the tunnel first, then copy its credentials here:" >&2
    echo >&2
    echo "    cloudflared tunnel login" >&2
    echo "    cloudflared tunnel create ecommerce-${OVERLAY}" >&2
    echo "    cp ~/.cloudflared/<id>.json ${credentials}" >&2
    exit 1
fi

printf '\ncommit the sealedsecret.yaml files — the material beside them stays out of git.\n'
