# gcr-notifier

A tiny application to publish [Google Cloud Container Builder](https://cloud.google.com/container-builder/) status notifications to github.

# Installation
```
git clone https://github.com/amcleodca/gcr-notifier && cd gcr-notifier
make deps
make
```

# Usage
```
GITHUB_ACCESS_TOKEN=xxx_your_github_token_xxx  \
PROJECT_ID=xxx_your_gcp_project_id_xxx \
./bin/gcr-notifier
```

# Deployment to kubernetes
Build `Dockerfile`. See `config/deploy/production` for an example of k8s resources that can be deployed with [kubernetes-deploy](https://github.com/Shopify/kubernetes-deploy) 
