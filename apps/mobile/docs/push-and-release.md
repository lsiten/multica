# Native APNs and iOS distribution

The iOS client uses Apple's APNs device token and the self-hosted Go backend.
It does not use Expo Push Service. `expo-notifications` supplies native iOS
permission, token, and notification-response APIs.

## Build prerequisites

Push-enabled builds require a paid Apple Developer team, an explicit App ID
matching the selected bundle identifier, and the Push Notifications capability
enabled for that App ID. Set `EXPO_ENABLE_APNS=1` before prebuild/build. The
default leaves the entitlement disabled so free Apple-ID self-builds continue
to work. Use a physical device for delivery verification.

The client reads the signed binary's APNs environment through
`Application.getIosPushNotificationServiceEnvironmentAsync()`. It does not
infer sandbox/production from `APP_ENV`: a staging TestFlight binary uses
production APNs; a development-signed build uses sandbox APNs.

After changing the push capability, regenerate and rebuild the native project.
When disabling a capability in an existing generated project, save local
native customizations and run `expo prebuild --clean --platform ios` once to
remove stale entitlements. The normal build loop continues to use
`scripts/ios-run.sh` with incremental prebuild.

## Backend configuration

Create an APNs token-signing P8 key in the Apple Developer account. Configure
each environment authorized by that key:

```dotenv
MULTICA_APNS_TEAM_ID=ABCDE12345
MULTICA_APNS_TOPICS=ai.multica.mobile,ai.multica.mobile.staging
MULTICA_APNS_PRODUCTION_KEY_ID=ABCDE12345
MULTICA_APNS_PRODUCTION_PRIVATE_KEY_FILE=/etc/multica/apns/production.p8
MULTICA_APNS_SANDBOX_KEY_ID=ABCDE12345
MULTICA_APNS_SANDBOX_PRIVATE_KEY_FILE=/etc/multica/apns/sandbox.p8
```

These values are examples. The topic must exactly match the signed bundle ID,
including a personal override. Omit unneeded environments. Keep P8 contents
out of the repository and client bundle. Allow outbound HTTP/2 over TLS to
`api.push.apple.com:443` and `api.sandbox.push.apple.com:443`.

Self-host Compose forwards these variables. Mount the files with a local
override, using the backend service's actual container path:

```yaml
services:
  backend:
    volumes:
      - /secure/apns:/etc/multica/apns:ro
```

For Helm, create a Secret containing `production.p8` and/or `sandbox.p8`, set
`backend.apnsCredentialsSecret` to its name, and put the environment values in
the existing backend environment Secret. The chart mounts keys read-only at
`/etc/multica/apns`. Restart the backend after key rotation (a Helm upgrade
also checksums the referenced Secret).

The authenticated `GET /api/mobile-push/config` reports only availability,
allowed topics and environments. Missing configuration disables registration
with an actionable state in mobile Settings → Notifications. Registration
is human-only and account-scoped. The same native token transfers to the new
account on sign-in; another account cannot deregister it.

The worker scans persisted unread inbox items and completed direct-chat replies,
so restarting the backend does not lose the delivery source. Only items newer
than the device's current account registration and less than 24 hours old are
eligible. Delivery rechecks membership, private-project access, read state,
and workspace notification preferences. Channel-ingested chat replies and
agent-introduction chats are excluded. Transient failures retry up to six
attempts; invalid APNs tokens are removed. Delivered alerts can remain in
iOS Notification Center after the item is read on another client. APNs alerts
are best effort, and a crash after Apple's acceptance can cause a repeat;
the collapse ID reduces duplicate pending alerts.

## TestFlight and production updates

EAS is an optional build/submission service. Existing local Xcode device builds
remain available; APNs delivery always goes directly from the deployment's
backend to Apple, regardless of how the binary was built.

`eas.json` defines `testflight` (staging variant, EAS `preview` environment)
and `production` (EAS `production` environment) store profiles,
both with APNs enabled and remotely incremented iOS build numbers. They require
the deployment owner's EAS project and Apple signing credentials. Before the
first build, configure EAS for this app and set `EXPO_EAS_PROJECT_ID` to that
project's UUID (the dynamic app config reads it). Set `EXPO_PUBLIC_API_URL` and
`EXPO_PUBLIC_WEB_URL` in those respective EAS environments. Local dotenv-cli scripts
load the committed variant files; EAS does not run those scripts. Use the
same bundle ID and Apple team in EAS and the server's topic allowlist.

From `apps/mobile`, with the owner's approved EAS CLI and authenticated account:

```bash
eas build --platform ios --profile testflight
eas submit --platform ios --profile testflight --latest
```

Choose the staging App Store Connect application at submission. Production
uses `--profile production` and the production App Store Connect application.
Submission uploads a binary; it does not approve an App Store release. Provide
the deployment's required privacy disclosures and review information in App
Store Connect. EAS Submit supports supplying the app's `ascAppId` in the local
submission configuration when interactive selection is inappropriate.

Updates ship as new signed binaries through TestFlight/App Store (or the existing
local device build scripts for self-builds). OTA updates are not configured.
Install a newer build over the existing app to verify token/session preservation.
Restore a prior reviewed binary through the distribution channel if rollback is
needed; do not change a live client's backend URL through an unsigned update.

## Required release evidence

Verify permission granted/denied and recovery from iOS Settings; foreground
inbox updates without duplicate system banners; background and terminated-app
delivery; cold/warm notification taps to the correct workspace, issue, human
request and chat; logout/account switch; rotated token; read/muted/revoked
access; offline retry; and an upgrade over the prior build. Repeat using a
development-signed sandbox build and a TestFlight production-APNs build.

The current automated suite covers protocol signing/HTTP2, device ownership,
queue authorization/preferences/retries, native registration lifecycle and
destination parsing. It does not establish Apple credential validity, signing,
real-device delivery, native rendering, or TestFlight availability.

References: [Apple APNs requests](https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns),
[Expo notification APIs](https://docs.expo.dev/versions/v57.0.0/sdk/notifications/),
[EAS iOS submission](https://docs.expo.dev/submit/ios/),
[EAS build versions](https://docs.expo.dev/build-reference/app-versions/).
