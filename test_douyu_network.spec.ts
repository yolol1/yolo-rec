import { test, expect } from '@playwright/test';

test('capture douyu network', async ({ page }) => {
  // Catch all responses
  page.on('response', async (response) => {
    const url = response.url();
    if (url.includes('/lapi/live/getH5Play') || url.includes('/lapi/live/') || url.includes('playweb') || url.includes('rate')) {
      console.log('=== Intercepted API Response ===');
      console.log('URL:', url);
      try {
        const body = await response.json();
        console.log('Body:', JSON.stringify(body).substring(0, 500)); // Print up to 500 chars
      } catch (e) {
        console.log('Body: Not JSON or could not read');
      }
    }
  });

  // Navigate to a popular room
  await page.goto('https://www.douyu.com/9999', { waitUntil: 'networkidle', timeout: 30000 });
  
  // Wait a few seconds for the video to load and APIs to be called
  await page.waitForTimeout(5000);

  console.log('Finished capturing.');
});
