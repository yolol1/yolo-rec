const { chromium } = require('playwright');

(async () => {
  const browser = await chromium.launch({ 
    headless: true,
    args: ['--disable-blink-features=AutomationControlled']
  });
  const context = await browser.newContext({
    userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'
  });
  
  const cookieStr = "dy_did=33040a51931ff4aa775496ac00081701; dy_did=33040a51931ff4aa775496ac00081701; acf_web_id=7530868976193140495; acf_ab_pmt=20100212%23webnewhome%23B%2C20100395%23webslidetag%23B%2C20100249%23webTagRank%23B%2C20100248%23webTagHover%23B%2C20100254%23WebTool0703%23new%2C20100272%23all_lists_sort%23c; acf_ab_ver_all=20100212%2C20100395%2C20100249%2C20100248%2C20100254%2C20100272; acf_ab_vs=webnewhome%3DB%2Cwebslidetag%3DB%2CwebTagRank%3DB%2CwebTagHover%3DB%2CWebTool0703%3Dnew%2Call_lists_sort%3Dc; dy_teen_mode=%7B%22uid%22%3A%22328379366%22%2C%22status%22%3A0%2C%22birthday%22%3A%22%22%2C%22password%22%3A%22%22%7D; acf_did=33040a51931ff4aa775496ac00081701; _ga_5JKQ7DTEXC=GS2.1.s1772623595$o7$g1$t1772624439$j60$l0$h295898765; _ga=GA1.1.60130214.1724785640; Hm_lvt_e99aee90ec1b2106afe7ec3b199020a7=1772623598; acf_ssid=1729386654963271219; mantine-color-scheme-value=dark; game_did=yVeHidRi2opi0DZ50EfrKQPUUc3DcP7rRCT; huya_ua=outer_pc&0.0.1&websocket&&h5_/10106809; guid=0a89c7f8911672693d01566d576d4af4; Hm_lpvt_e99aee90ec1b2106afe7ec3b199020a7=1772623598; HMACCOUNT=E8FAECBEB6603CDF; PHPSESSID=mtuhtoncqlc8rs82432jh76rp0; _TDID_CK=1761038849792; 6333762c95037d16=XI88VVThULw9jPQrDqSpXa1x5F7REKCEcu7ll4ZnhwZ6yV0KUqb0n%2BKnRiIHI%2FX7XTGPkJtb%2F0OW2a0d6XSTCOoqFRo9eOXAQB8jQs94sufyIgUGdK1%2FxCI%2FAYBLC3PN2Qg8IHnmkt%2BJLJTxam1dK3EN02Zfep2l3Cke0G%2BdJQ0%2BWjLCrdazG0%2BY55MJ1IpsBLl9Fg%2Boh0ZTekAcGnfBCrd7YCeRHcysOhzmR%2FT7iyMDDDlMrxHq8KT8DuSeKcY4Va1sxUKYGoGGz3wOfdC0dJcUv0pwgnnxO5DS9C7XOOz2s2pJksVPug%3D%3D; acf_avatar=https%3A%2F%2Fapic.douyucdn.cn%2Fupload%2Favatar_v3%2F202307%2F37058299d92f4fb39177515ad882fc23_; post-csrfToken=tnrvd3n8zan; acf_auth=e6c3Mc5mnBEID8zkAIPJfddDTF5J2OH52sBpuVAvx3EwY4ISbRPMJKtLs%2B%2BMUFgkqS%2Be3mvc5UjkofbO7uYD4EKMKYjNrQryacE%2Fhu41YARg%2F23pu4cgk7k; acf_jwt_token=eyJhbGciOiJtZDUiLCJ0eXAiOiJKV1QifQ.eyJjdCI6MCwiaWF0IjoxNzc5NTM4MjE5LCJhdWQiOlsiZHkiXSwibHRraWQiOjk3NTEzMDUxLCJiaXoiOjEsInVpZCI6MzI4Mzc5MzY2LCJleHAiOjE3ODAxNDMwMTksInN1YiI6InN0Iiwia2V5IjoiZHktand0LW1kNSIsInN0ayI6Ijg1ZTJlY2YwOTY0NzBiNTgifQ.MDgwMTA3OGVjZGJkMjM4ODIwNGJhY2NjNzg2MDM3OGI; acf_dmjwt_token=eyJhbGciOiJtZDUiLCJ0eXAiOiJKV1QifQ.eyJjdCI6MCwiaWF0IjoxNzc5NTM4MjE5LCJhdWQiOlsiZG0iXSwibHRraWQiOjk3NTEzMDUxLCJiaXoiOjEsInVpZCI6MzI4Mzc5MzY2LCJleHAiOjE3ODAxNDMwMTksInN1YiI6InN0Iiwia2V5IjoiZHktand0LW1kNSIsInN0ayI6Ijg1ZTJlY2YwOTY0NzBiNTgifQ.OTViZjM2ZDg2ZTBjOWE2ZTI3OWU4MTBmZWYzZjQ5ZGQ; dy_auth=05c9HSJTDX%2FD1tM04Bws5G1BG2HdobUVvJpuaNqvReMVo%2FUiqfcEAYidJ0YJZdHbpbzyeJDMg0xpd4L5KJnrcolFHL%2BT6p88m6oZVOCQYT5xLWp%2F7MKmL08; wan_auth37wan=f09f207a067bEjDPVe0DcLD5sGiigIxNeKtfMyW6s8LmZ7WNMSRGzoAMQAiBffOkMkhlAJQR4UwsYwbT6mHfKP4xV0rEZ1zyRob9GsC6Q%2BeCdZHbq4k; acf_uid=328379366; acf_username=328379366; acf_nickname=%E5%B0%8F%E7%9B%86%E5%8F%8B%E5%90%83%E9%9B%AA%E7%B3%95; acf_own_room=0; acf_groupid=1; acf_phonestatus=1; acf_ct=0; acf_ltkid=97513051; acf_biz=1; acf_stk=85e2ecf096470b58; acf_ccn=6588b973667da134b9bb3788de2fcb41";
  const cookies = cookieStr.split(';').map(pair => {
    let [name, ...value] = pair.trim().split('=');
    return {
      name: name,
      value: value.join('='),
      domain: '.douyu.com',
      path: '/'
    };
  });
  await context.addCookies(cookies);
  
  const page = await context.newPage();
  await page.route('**/lapi/live/getH5Play**', async (route) => {
    const request = route.request();
    let postData = request.postData() || '';
    if (postData.includes('hevc=0')) {
      console.log('Replacing hevc=0 with hevc=1');
      postData = postData.replace('hevc=0', 'hevc=1');
    }
    console.log('=== Modified API Request ===');
    console.log('Post Data:', postData);
    route.continue({ postData });
  });

  page.on('response', async (response) => {
    const url = response.url();
    if (url.includes('/lapi/live/getH5Play')) {
      console.log('=== API Response ===');
      try {
        const body = await response.json();
        console.log('Body:', JSON.stringify(body).substring(0, 1000));
      } catch (e) {
      }
    }
  });

  console.log('Navigating to Douyu...');
  await page.goto('https://www.douyu.com/9999', { waitUntil: 'networkidle', timeout: 45000 });
  console.log('Page loaded. Waiting for streams...');
  await page.waitForTimeout(10000); // wait 10s for streams
  
  await browser.close();
  console.log('Done.');
})();
