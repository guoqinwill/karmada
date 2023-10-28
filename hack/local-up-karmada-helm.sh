#!/bin/bash
set -ex

## This script deploy karmada control plane to a Kind cluster using helm chart, and then deploy three member clusters.
## Two member clusters are joined in Push mode, using karmadactl join command.
## Another member cluster is joined in Pull mode, using helm chart of karmada-agent.
## After installation, you can access to each cluster by following kubeconfig:
## - karmada-host ~/.kube/karmada-host.config
## - karmada-apiserver ~/.kube/karmada-apiserver.config
## - member1/member2/member3 ~/.kube/members.config

# 1. clean old Kind clusters
kind delete clusters karmada-host member1 member2 member3
rm -rf ~/.karmada ~/.kube/*config /etc/karmada

# 2. create karmada-host cluster by Kind
hack/create-cluster.sh karmada-host ~/.kube/karmada-host.config

# (optional) pull images in advance for karmada-host
imgs=$(cat charts/karmada/values.yaml | grep -C 1 'repository:' | sed 's/*karmadaImageVersion/latest/g' | awk -F ':' '{print $2}' | sed 's/\"//g' | xargs -n3 | awk '{print $1"/"$2":"$3}')
for img in ${imgs}; do
  docker pull "${img}"
  kind load docker-image "${img}" --name karmada-host
done

# 3. get host network ip (node ip) from kube-apiserver, and then add this ip to values.yaml as SAN of certificate
export KUBECONFIG=~/.kube/karmada-host.config
HOST_IP=$(kubectl get ep kubernetes -o jsonpath='{.subsets[0].addresses[0].ip}')
sed -i'' -e "/localhost/{n;s/      \"127.0.0.1/      \"${HOST_IP}\",\n&/g}" charts/karmada/values.yaml

# 4. install karmada in host cluster by helm
helm install karmada -n karmada-system \
        --kubeconfig ~/.kube/karmada-host.config \
        --create-namespace \
        --dependency-update \
        --set apiServer.hostNetwork=true,components={"search,descheduler,schedulerEstimator"} \
        ./charts/karmada

# 5. export kubeconfig of karmada-apiserver to local path
kubectl get secret -n karmada-system karmada-kubeconfig -o jsonpath={.data.kubeconfig} | base64 -d > ~/.kube/karmada-apiserver.config
KARMADA_APISERVER_ADDR=$(kubectl get ep karmada-apiserver -n karmada-system | tail -n 1 | awk '{print $2}')
sed -i'' -e "s/karmada-apiserver.karmada-system.svc.cluster.local:5443/${KARMADA_APISERVER_ADDR}/g" ~/.kube/karmada-apiserver.config

# 6. create member clusters by Kind
hack/create-cluster.sh member1 ~/.kube/members.config
hack/create-cluster.sh member2 ~/.kube/members.config
hack/create-cluster.sh member3 ~/.kube/members.config

# 7. make karmadactl binary
make karmadactl

# 8. join member1、member3 to karmada with push mode
karmadactl join member1 --kubeconfig ~/.kube/karmada-apiserver.config --karmada-context karmada-apiserver --cluster-kubeconfig ~/.kube/members.config --cluster-context member1
karmadactl join member3 --kubeconfig ~/.kube/karmada-apiserver.config --karmada-context karmada-apiserver --cluster-kubeconfig ~/.kube/members.config --cluster-context member3

# (optional) pull agent image in advance for member2
docker pull docker.io/karmada/karmada-agent:latest; kind load docker-image docker.io/karmada/karmada-agent:latest --name member2

# 9. get cert from karmada config for agent
set +x
CA_CRT=$(cat ~/.kube/karmada-apiserver.config | grep certificate-authority-data | awk -F ': ' '{print $2}' | base64 -d)
AGENT_CRT=$(cat ~/.kube/karmada-apiserver.config | grep client-certificate-data | awk -F ': ' '{print $2}' | base64 -d)
AGENT_KEY=$(cat ~/.kube/karmada-apiserver.config | grep client-key-data | awk -F ': ' '{print $2}' | base64 -d)

# 10. install karmada-agent in member2 cluster, and then join member2 to karmada with pull mode
helm install karmada-agent -n karmada-system \
        --kubeconfig ~/.kube/members.config \
        --kube-context member2 \
        --create-namespace \
        --dependency-update \
        --set installMode=agent,agent.clusterName=member2,agent.kubeconfig.server=https://"${KARMADA_APISERVER_ADDR}",agent.kubeconfig.caCrt="${CA_CRT}",agent.kubeconfig.crt="${AGENT_CRT}",agent.kubeconfig.key="${AGENT_KEY}" \
        ./charts/karmada

# verify: wait for member cluster ready and then print member clusters
while [[ "$(kubectl --kubeconfig ~/.kube/karmada-apiserver.config get cluster -o wide | wc -l)" -ne 4 ]]; do
  echo "waiting for member clusters ready..."; sleep 2;
done
kubectl --kubeconfig ~/.kube/karmada-apiserver.config get cluster -o wide
