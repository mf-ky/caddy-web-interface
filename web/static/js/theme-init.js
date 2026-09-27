// Applies the saved theme before first paint to avoid a light/dark flash.
try { var t = localStorage.getItem('caddyweb-theme'); if (t && t !== 'system') document.documentElement.setAttribute('data-theme', t); } catch (e) { /* storage blocked */ }
