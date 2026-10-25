# Get energy consumption

```sh
curl \
--silent --show-error --fail-with-body \
--insecure \
--header "Authorization: Bearer <access-token>" \
"https://<euer-wärmepumpen-hostname>:9443/heatSources/emon/totalConsumption"
  ```

Further paths: `/heatSources/emon/chConsumption`, `/heatSources/emon/dhwConsumption`, `/heatSources/emon/coolingConsumption`

```sh
curl --silent --insecure \
--header "Authorization: Bearer <access-token>" \
"https://192.168.2.97:9443/heatSources/emon/chConsumption"
  ```

# Get energy recording

```sh
curl \
--silent --show-error --fail-with-body \
--insecure \
--get "https://<euer-wärmepumpen-hostname>:9443/recordings/heatSources/emon/total/electricity" \
--header "Authorization: Bearer <access-token>" \
--data-urlencode "sampleRate=all" \
--data-urlencode "startDate=<yyyy-mm-dd>" \
--data-urlencode "endDate=<yyyy-mm-dd>"
  ```


