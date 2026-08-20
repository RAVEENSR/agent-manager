// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package auth

import (
	"html"
	"strings"
)

// loginPageTemplate follows the console's Oxygen UI Acrylic Orange theme.
const loginPageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{title}}</title>
<style>
  :root { color-scheme: light dark; }
  * { margin: 0; box-sizing: border-box; }
  body {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    font-family: 'Inter Variable', Inter, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
    background-color: #f5f5f5;
    color: #40404b;
    background-image:
      radial-gradient(circle at 65% 30%, rgba(255, 116, 0, 0.10) 10%, rgba(255, 255, 255, 0) 40%),
      radial-gradient(circle at 15% 50%, rgba(74, 41, 165, 0.10) 1%, rgba(255, 255, 255, 0) 40%);
    background-attachment: fixed;
  }
  main {
    background: rgba(255, 255, 255, 0.77);
    border: 1px solid rgba(0, 0, 0, 0.07);
    border-radius: 12px;
    box-shadow: 0 4px 24px rgba(0, 0, 0, 0.06);
    -webkit-backdrop-filter: blur(12px);
    backdrop-filter: blur(12px);
    padding: 48px 56px;
    margin: 16px;
    text-align: center;
    max-width: 420px;
  }
  .icon {
    width: 56px;
    height: 56px;
    border-radius: 50%;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    margin-bottom: 20px;
    color: #ffffff;
  }
  .icon.success { background: linear-gradient(90deg, #f47b20 0%, #ef4223 100%); }
  .icon.error { background: #d3302f; }
  h1 { font-size: 1.25rem; font-weight: 600; margin-bottom: 8px; }
  p { font-size: 0.875rem; color: #6b6b76; line-height: 1.5; overflow-wrap: break-word; }
  @media (prefers-color-scheme: dark) {
    body {
      background-color: #000000;
      color: #efefef;
      background-image:
        radial-gradient(circle at 65% 30%, rgba(255, 116, 0, 0.13) 10%, rgba(0, 0, 0, 0) 60%),
        radial-gradient(circle at 15% 50%, rgba(132, 40, 0, 0.18) 1%, rgba(0, 0, 0, 0) 40%);
    }
    main { background: rgba(0, 0, 0, 0.77); border-color: rgba(255, 255, 255, 0.09); }
    p { color: #d0d3e2; }
  }
</style>
</head>
<body>
<main>
  <div class="icon {{status}}">{{glyph}}</div>
  <h1>{{heading}}</h1>
  <p>{{message}}</p>
</main>
</body>
</html>`

const (
	checkGlyph = `<svg viewBox="0 0 24 24" width="28" height="28" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>`
	crossGlyph = `<svg viewBox="0 0 24 24" width="28" height="28" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6 6 18M6 6l12 12"/></svg>`
)

func loginSuccessPage() string {
	return renderLoginPage("Login Successful", "success", checkGlyph, "Login successful",
		"You can close this tab and return to the terminal.")
}

func loginErrorPage(message string) string {
	return renderLoginPage("Login Failed", "error", crossGlyph, "Login failed", message)
}

func renderLoginPage(title, status, glyph, heading, message string) string {
	return strings.NewReplacer(
		"{{title}}", title,
		"{{status}}", status,
		"{{glyph}}", glyph,
		"{{heading}}", heading,
		"{{message}}", html.EscapeString(message),
	).Replace(loginPageTemplate)
}
