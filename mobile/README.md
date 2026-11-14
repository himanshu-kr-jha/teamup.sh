# TeamUp phone app

The phone app (Android now, iPhone from the same project) is a native shell around the TeamUp web app of one server.
It loads that server's pages instead of carrying its own copy, so every deploy of the web app shows up in the app straight away.
The app and the website cannot drift apart, and only a change to the shell itself needs a new store release.

The shell adds what a browser tab cannot:

- its own icon, launch screen and full-screen window, with no install step;
- camera and microphone for calls, asked for the first time somebody joins one;
- downloads saved to the phone's Downloads folder, with a notification;
- a "Can't reach TeamUp" page with Try again when the phone is offline or the server is down.

Inside the app the web app knows where it is: the user agent ends in `TeamUpApp`, and `isPhoneApp()` in `web/src/features/install/install.ts` reads it.

## Build it

You need Node 20 or newer, JDK 21 and the Android SDK (Android Studio installs it).

```bash
cd mobile
npm ci
TEAMUP_APP_URL=https://teamup.example.com npm run android
```

The app is `android/app/build/outputs/apk/debug/app-debug.apk`.
Copy it to an Android phone and open it to install, after allowing installs from that source.

`TEAMUP_APP_URL` is the server the app opens: https, and no path.
It is fixed when the app is built, so a build for another server needs its own `TEAMUP_APP_URL`.

## Publish it

- **Store id and name.** The app is `sh.teamup.app`, called TeamUp.
  To publish your own, change `appId` in `capacitor.config.ts`, `applicationId` and `namespace` in `android/app/build.gradle`, the package of `MainActivity.java`, and `app_name` in `android/app/src/main/res/values/strings.xml`, before the first upload.
  A store id can never change after that.
- **Google Play.** A Play Console account costs US$25 once.
  Make an upload key, keep it out of the repository (`*.keystore`, `*.jks` and `keystore.properties` are ignored), and build with `npm run android:release`.
- **App Store.** Needs a Mac with Xcode and an Apple Developer account at US$99 a year.
  Run `npx cap add ios` on the Mac, then open `ios/App` in Xcode.

## Still to come

- **Push notifications.** The web app's push does not reach an app, so these need Firebase Cloud Messaging and a matching sender on the server.
- **Sign in with Google or Microsoft.** Both refuse to sign in inside an app's web view, so these need the phone's browser and a hand-back to the app.
  Signing in with a password works now.
- **Saving files made in the page,** such as an exported list. The app says to use a browser for those for now.
