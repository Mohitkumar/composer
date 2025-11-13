#!/bin/bash
# Bootstrap script for Kubernetes deployment
# This script sets up Composer in a Kubernetes cluster using Helm

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
HELM_CHART_DIR="$PROJECT_ROOT/infra/k8s/helm/composer"

# Default values
RELEASE_NAME="composer"
NAMESPACE="composer"
VALUES_FILE=""
DRY_RUN=false
UPGRADE=false

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -n|--namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        -r|--release)
            RELEASE_NAME="$2"
            shift 2
            ;;
        -f|--values)
            VALUES_FILE="$2"
            shift 2
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --upgrade)
            UPGRADE=true
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  -n, --namespace NAME    Kubernetes namespace (default: composer)"
            echo "  -r, --release NAME      Helm release name (default: composer)"
            echo "  -f, --values FILE       Path to custom values file"
            echo "  --dry-run               Perform a dry run without installing"
            echo "  --upgrade               Upgrade existing release"
            echo "  -h, --help              Show this help message"
            exit 0
            ;;
        *)
            echo -e "${RED}Unknown option: $1${NC}"
            echo "Use -h or --help for usage information"
            exit 1
            ;;
    esac
done

echo -e "${GREEN}=== Composer Kubernetes Deployment Bootstrap ===${NC}\n"

# Check prerequisites
echo -e "${YELLOW}Checking prerequisites...${NC}"

# Check kubectl
if ! command -v kubectl &> /dev/null; then
    echo -e "${RED}Error: kubectl is not installed. Please install kubectl first.${NC}"
    exit 1
fi

# Check Helm
if ! command -v helm &> /dev/null; then
    echo -e "${RED}Error: Helm is not installed. Please install Helm first.${NC}"
    echo -e "${YELLOW}Installation: https://helm.sh/docs/intro/install/${NC}"
    exit 1
fi

# Check Kubernetes connection
if ! kubectl cluster-info &> /dev/null; then
    echo -e "${RED}Error: Cannot connect to Kubernetes cluster.${NC}"
    echo -e "${YELLOW}Please ensure kubectl is configured correctly.${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Prerequisites check passed${NC}\n"

# Display cluster information
echo -e "${BLUE}Cluster Information:${NC}"
kubectl cluster-info | head -1
echo -e "Context: ${YELLOW}$(kubectl config current-context)${NC}"
echo ""

# Create namespace if it doesn't exist
if ! kubectl get namespace "$NAMESPACE" &> /dev/null; then
    echo -e "${YELLOW}Creating namespace: $NAMESPACE${NC}"
    kubectl create namespace "$NAMESPACE"
else
    echo -e "${GREEN}Namespace '$NAMESPACE' already exists${NC}"
fi

# Check if release already exists
if helm list -n "$NAMESPACE" | grep -q "^$RELEASE_NAME"; then
    if [ "$UPGRADE" = false ] && [ "$DRY_RUN" = false ]; then
        echo -e "${YELLOW}Release '$RELEASE_NAME' already exists in namespace '$NAMESPACE'${NC}"
        read -p "Do you want to upgrade it? (y/N): " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            UPGRADE=true
        else
            echo -e "${YELLOW}Exiting. Use --upgrade flag to upgrade existing release.${NC}"
            exit 0
        fi
    fi
fi

# Prepare Helm command
HELM_CMD="helm"
if [ "$UPGRADE" = true ]; then
    HELM_CMD="$HELM_CMD upgrade"
else
    HELM_CMD="$HELM_CMD install"
fi

HELM_CMD="$HELM_CMD $RELEASE_NAME $HELM_CHART_DIR"
HELM_CMD="$HELM_CMD --namespace $NAMESPACE"

if [ -n "$VALUES_FILE" ]; then
    if [ ! -f "$VALUES_FILE" ]; then
        echo -e "${RED}Error: Values file not found: $VALUES_FILE${NC}"
        exit 1
    fi
    HELM_CMD="$HELM_CMD -f $VALUES_FILE"
fi

if [ "$DRY_RUN" = true ]; then
    HELM_CMD="$HELM_CMD --dry-run --debug"
    echo -e "${YELLOW}Running in dry-run mode...${NC}\n"
else
    echo -e "${YELLOW}Deploying Composer to Kubernetes...${NC}\n"
fi

# Execute Helm command
eval "$HELM_CMD"

if [ "$DRY_RUN" = true ]; then
    echo -e "\n${GREEN}✓ Dry-run completed successfully${NC}"
    echo -e "${YELLOW}Remove --dry-run flag to perform actual installation.${NC}"
    exit 0
fi

# Wait for deployments to be ready
echo -e "\n${YELLOW}Waiting for deployments to be ready...${NC}"
kubectl wait --for=condition=available --timeout=300s \
    deployment/composer-coordinator \
    -n "$NAMESPACE" 2>/dev/null || true

# Display deployment status
echo -e "\n${GREEN}=== Deployment Status ===${NC}"
kubectl get pods -n "$NAMESPACE" -l "app.kubernetes.io/instance=$RELEASE_NAME"

# Display service information
echo -e "\n${GREEN}=== Service Information ===${NC}"
kubectl get svc -n "$NAMESPACE" -l "app.kubernetes.io/instance=$RELEASE_NAME"

# Display access information
echo -e "\n${GREEN}=== Access Information ===${NC}"
echo -e "To access services, use port-forward:"
echo -e ""
echo -e "Coordinator HTTP:"
echo -e "  ${YELLOW}kubectl port-forward -n $NAMESPACE svc/$RELEASE_NAME-coordinator 8080:8080${NC}"
echo -e ""
echo -e "Jaeger UI:"
echo -e "  ${YELLOW}kubectl port-forward -n $NAMESPACE svc/$RELEASE_NAME-jaeger-query 16686:16686${NC}"
echo -e "  Then visit: ${GREEN}http://localhost:16686${NC}"
echo -e ""
echo -e "Prometheus UI:"
echo -e "  ${YELLOW}kubectl port-forward -n $NAMESPACE svc/prometheus-server 9090:9090${NC}"
echo -e "  Then visit: ${GREEN}http://localhost:9090${NC}"
echo -e ""
echo -e "Grafana UI:"
echo -e "  ${YELLOW}kubectl port-forward -n $NAMESPACE svc/$RELEASE_NAME-grafana 3000:3000${NC}"
echo -e "  Then visit: ${GREEN}http://localhost:3000${NC} (admin/admin)"

echo -e "\n${GREEN}=== Useful Commands ===${NC}"
echo -e "View logs:           ${YELLOW}kubectl logs -n $NAMESPACE -l app.kubernetes.io/instance=$RELEASE_NAME -f${NC}"
echo -e "View all resources:   ${YELLOW}kubectl get all -n $NAMESPACE -l app.kubernetes.io/instance=$RELEASE_NAME${NC}"
echo -e "Uninstall:           ${YELLOW}helm uninstall $RELEASE_NAME -n $NAMESPACE${NC}"
echo -e "Upgrade:              ${YELLOW}helm upgrade $RELEASE_NAME $HELM_CHART_DIR -n $NAMESPACE${NC}"

echo -e "\n${GREEN}✓ Composer has been deployed to Kubernetes!${NC}\n"
