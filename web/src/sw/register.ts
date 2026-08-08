// Service Worker registration
import { registerSW } from 'virtual:pwa-register';

export function registerServiceWorker() {
  if ('serviceWorker' in navigator) {
    const updateSW = registerSW({
      onNeedRefresh() {
        if (confirm('New version available. Update?')) {
          updateSW(true);
        }
      },
      onOfflineReady() {
        console.log('App ready for offline use');
        // Could show a toast here
      },
      onRegistered(registration) {
        registration?.addEventListener('updatefound', () => {
          console.log('SW update found');
        });
      },
      onRegisterError(error) {
        console.error('SW registration failed:', error);
      },
    });
  }
}
