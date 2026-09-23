#!/bin/bash
# Only deploy website - do not touch services
docker cp galaxia-agent:/var/www/alphanetx/index.html /var/www/alphanetx/index.html 2>/dev/null
echo "$(date): website deployed" >> /var/log/auto-deploy.log
