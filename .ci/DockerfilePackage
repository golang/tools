
ARG VERSION=10.15.2
FROM node:${VERSION}

USER root

RUN set -ex && \
    apt-get update && apt-get -y upgrade && \
    apt-get install -y jq make python-dev curl && \
    wget -O get-pip.py 'https://bootstrap.pypa.io/get-pip.py' && \
    python get-pip.py --disable-pip-version-check --no-cache-dir && \
    rm -f get-pip.py && \
    pip --no-cache-dir install --upgrade awscli==1.14.5 s3cmd==2.0.1 python-magic && \
    apt-get -qq -y autoremove && \
    apt-get -qq -y clean && \
    rm -rf /var/lib/apt/lists/*


# Update the current node user id (1000)
# because it might be the CI user id we want
# to create.
RUN usermod -u 15000 node

####
# Build CI User
###
ARG CI_USER_UID=1000
ENV CI_USER ciagent
ENV CI_GROUP ciagent
ENV HOME /home/${CI_USER}

# - Create user and group with specific ids
# - Create needed directories
RUN useradd --create-home --user-group -u ${CI_USER_UID} --shell /bin/bash ${CI_USER} \
   && mkdir -p ${HOME}/.aws  \
   && mkdir -p ${HOME}/.m2 \
   && chown -R ${CI_USER}:${CI_GROUP} ${HOME}

USER ${CI_USER}

VOLUME /home/ciagent/.aws
VOLUME /home/ciagent/.m2
VOLUME /plugin/kibana
VOLUME /plugin/kibana-extra/go-langserver

WORKDIR /plugin/kibana-extra/go-langserver
