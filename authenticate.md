# Get access token

```sh
curl \
--silent --show-error --fail-with-body \
--insecure \
--request POST "https://<euer-wärmepumpen-hostname>:9442/auth/token" \
--header "Content-Type: application/x-www-form-urlencoded" \
--data-urlencode "grant_type=password" \
--data-urlencode "username=<login-vom-aufkleber>" \
--data-urlencode "password=<pass-vom-aufkleber-ohne-bindestriche>" \
--data-urlencode "client_name=<ein-beliebiger-name-eures-clients>"
  ```


