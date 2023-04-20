#
# Licensed to the Apache Software Foundation (ASF) under one or more
# contributor license agreements.  See the NOTICE file distributed with
# this work for additional information regarding copyright ownership.
# The ASF licenses this file to You under the Apache License, Version 2.0
# (the "License"); you may not use this file except in compliance with
# the License.  You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

# Do not fix the patch level for golang:1.20 to automatically get security fixes.
FROM golang:1.20-bullseye

# install the functions-deployer
ARG DEPLOYER_DOWNLOAD
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
RUN curl -L ${DEPLOYER_DOWNLOAD} | tar xzf - \
  && rm -fr /usr/local/lib/dosls && mv dosls /usr/local/lib \
  && rm -f /usr/local/bin/dosls && ln -s /usr/local/lib/dosls/bootstrap /usr/local/bin/dosls

RUN apt-get update \
  && apt-get install -y --no-install-recommends \
    # For tests, mostly.
    jq \
    # To run the compile script.
    python \
  && rm -rf /var/lib/apt/lists/* \
  && mkdir /action
WORKDIR /action

# This precompiles the standard library to make the build much quicker.
RUN GODEBUG=installgoroot=all go install -a std

COPY proxy /bin/proxy
COPY bin/compile /bin/compile
COPY lib/launcher.go /lib/launcher.go
COPY defaultBuild /bin/defaultBuild

ENV OW_COMPILER=/bin/compile
ENV OW_LOG_INIT_ERROR=1
ENV OW_WAIT_FOR_ACK=1

ENTRYPOINT [ "/bin/proxy" ]
