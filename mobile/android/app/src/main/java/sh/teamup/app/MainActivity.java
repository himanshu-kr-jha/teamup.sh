package sh.teamup.app;

import android.app.DownloadManager;
import android.net.Uri;
import android.os.Bundle;
import android.os.Environment;
import android.webkit.CookieManager;
import android.webkit.URLUtil;
import android.widget.Toast;
import com.getcapacitor.BridgeActivity;

/**
 * The TeamUp web app in a native window. Everything people see comes from the
 * server; this class only adds what a WebView does not do by itself.
 */
public class MainActivity extends BridgeActivity {

    @Override
    public void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        // A WebView ignores downloads unless told what to do with them. Hand
        // them to the phone's own download manager, with the signed-in
        // session, so a saved file lands in Downloads with its own name and a
        // notification, as it would from a browser.
        getBridge().getWebView().setDownloadListener((url, userAgent, contentDisposition, mimeType, contentLength) -> {
            if (!url.startsWith("https://")) {
                // A file made in the page itself (blob:) has no address to fetch.
                Toast.makeText(this, R.string.download_unsupported, Toast.LENGTH_LONG).show();
                return;
            }
            String name = URLUtil.guessFileName(url, contentDisposition, mimeType);
            DownloadManager.Request request = new DownloadManager.Request(Uri.parse(url))
                .setMimeType(mimeType)
                .addRequestHeader("User-Agent", userAgent)
                .setTitle(name)
                .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
                .setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name);
            String cookies = CookieManager.getInstance().getCookie(url);
            if (cookies != null) request.addRequestHeader("Cookie", cookies);
            DownloadManager downloads = (DownloadManager) getSystemService(DOWNLOAD_SERVICE);
            downloads.enqueue(request);
            Toast.makeText(this, getString(R.string.download_started, name), Toast.LENGTH_SHORT).show();
        });
    }
}
