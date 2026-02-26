(function() {
  const log = document.getElementById('log');
  const messages = document.getElementById('messages');
  const input = document.getElementById('input');
  const sendBtn = document.getElementById('send');
  const badge = document.getElementById('status-badge');
  const footerInfo = document.getElementById('footer-info');
  const sessionSelect = document.getElementById('session-select');
  const newChatBtn = document.getElementById('new-chat');
  const delChatBtn = document.getElementById('del-chat');
  const agentAvatar = document.getElementById('agent-avatar');
  const agentNameEl = document.getElementById('agent-name');

  // ── SSE Connection ──
  let eventCount = 0;

  function connectSSE() {
    const es = new EventSource('/events');

    es.onmessage = function(e) {
      try {
        const data = JSON.parse(e.data);
        appendLog(data);
        eventCount++;
        updateFooter();

        // Update status badge.
        if (data.type === 'control') {
          if (data.message.toLowerCase().includes('paused')) {
            setBadge('PAUSED', 'badge-paused');
          } else if (data.message.toLowerCase().includes('resumed')) {
            setBadge('RUNNING', 'badge-running');
          }
        }
      } catch (err) {
        console.error('SSE parse error:', err);
      }
    };

    es.onopen = function() {
      footerInfo.textContent = 'Connected';
      updateFooter();
    };

    es.onerror = function() {
      footerInfo.textContent = 'Disconnected — reconnecting...';
      setBadge('OFFLINE', 'badge-stopped');
    };
  }

  function appendLog(data) {
    const line = document.createElement('div');
    line.className = 'log-line ev-' + (data.type || 'default');

    const time = data.time ? new Date(data.time).toLocaleTimeString() : '';
    const timeSpan = '<span class="log-time">[' + escapeHtml(time) + ']</span> ';
    line.innerHTML = timeSpan + escapeHtml(data.message);

    log.appendChild(line);
    log.scrollTop = log.scrollHeight;
  }

  function setBadge(text, cls) {
    badge.textContent = text;
    badge.className = 'badge ' + cls;
  }

  function updateFooter() {
    // Fetch current state for footer display + agent info.
    fetch('/state').then(r => r.json()).then(state => {
      const parts = ['Token #' + state.token_id];
      parts.push(eventCount + ' events');
      footerInfo.textContent = parts.join(' | ');

      // Update agent identity in header (once).
      if (state.agent_name && agentNameEl.textContent === 'Agent') {
        agentNameEl.textContent = state.agent_name;
        if (state.agent_avatar_url) {
          agentAvatar.innerHTML = '<img src="' + escapeHtml(state.agent_avatar_url) + '" alt="">';
        } else {
          agentAvatar.textContent = state.agent_name.charAt(0).toUpperCase();
        }
      }

      // Sync badge with pause state.
      if (state.paused) {
        setBadge('PAUSED', 'badge-paused');
      }
    }).catch(() => {});
  }

  // ── Chat ──
  let sending = false;

  async function sendMessage() {
    const text = input.value.trim();
    if (!text || sending) return;

    input.value = '';
    sending = true;
    sendBtn.disabled = true;
    document.querySelectorAll('.cmd-bar a[data-msg]').forEach(function(a) { a.classList.add('cmd-disabled'); });

    appendChatMessage('user', text);
    const loadingEl = appendChatMessage('loading', 'Thinking...');

    try {
      const resp = await fetch('/chat', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({message: text}),
      });
      const data = await resp.json();

      if (data.error) {
        loadingEl.className = 'msg msg-system';
        loadingEl.textContent = 'Error: ' + data.error;
      } else {
        loadingEl.className = 'msg msg-assistant';
        loadingEl.innerHTML = '<span class="msg-role">Agent:</span><div class="msg-content">' + (data.reply ? renderMarkdown(data.reply) : '<span style="color:#6e7681">(no response)</span>') + '</div>';
        if (data.action) {
          appendChatMessage('system', 'Action executed: ' + data.action);
        }
      }
    } catch (err) {
      loadingEl.className = 'msg msg-system';
      loadingEl.textContent = 'Connection error: ' + err.message;
    }

    sending = false;
    sendBtn.disabled = false;
    document.querySelectorAll('.cmd-bar a[data-msg]').forEach(function(a) { a.classList.remove('cmd-disabled'); });
    messages.scrollTop = messages.scrollHeight;
    loadSessions(); // Refresh session list (title may have changed).
    input.focus();
  }

  function appendChatMessage(role, text) {
    const div = document.createElement('div');
    if (role === 'user') {
      div.className = 'msg msg-user';
      div.innerHTML = '<span class="msg-role">You:</span> ' + escapeHtml(text);
    } else if (role === 'assistant') {
      div.className = 'msg msg-assistant';
      div.innerHTML = '<span class="msg-role">Agent:</span><div class="msg-content">' + renderMarkdown(text) + '</div>';
    } else if (role === 'system') {
      div.className = 'msg msg-system';
      div.textContent = text;
    } else if (role === 'loading') {
      div.className = 'msg msg-loading';
      div.textContent = text;
    }
    messages.appendChild(div);
    messages.scrollTop = messages.scrollHeight;
    return div;
  }

  function escapeHtml(s) {
    const el = document.createElement('span');
    el.textContent = s;
    return el.innerHTML;
  }

  // Lightweight markdown → HTML renderer (safe: escapes HTML first).
  function renderMarkdown(raw) {
    if (!raw) return '';
    let s = escapeHtml(raw);

    // Code blocks: ```...```
    s = s.replace(/```(\w*)\n?([\s\S]*?)```/g, function(_, lang, code) {
      return '<pre><code>' + code.trim() + '</code></pre>';
    });

    // Inline code: `...`
    s = s.replace(/`([^`]+)`/g, '<code>$1</code>');

    // Bold: **...** (supports multiline)
    s = s.replace(/\*\*([\s\S]+?)\*\*/g, '<strong>$1</strong>');

    // Italic: *...* (runs after bold, so remaining * pairs are italic)
    s = s.replace(/\*(.+?)\*/g, '<em>$1</em>');

    // Horizontal rule: ---
    s = s.replace(/^-{3,}$/gm, '<hr>');

    // Unordered list items: - item
    s = s.replace(/^- (.+)$/gm, '<li>$1</li>');
    s = s.replace(/((?:<li>.*<\/li>\n?)+)/g, '<ul>$1</ul>');

    // Double newlines → paragraph break
    s = s.replace(/\n{2,}/g, '<br><br>');

    // Single newlines → line break
    s = s.replace(/\n/g, '<br>');

    return s;
  }

  // Send a preset message from quick buttons.
  function sendQuick(msg) {
    if (sending) return;
    input.value = msg;
    sendMessage();
  }

  // ── Sessions ──
  let currentSessionId = '';

  async function loadSessions() {
    try {
      const resp = await fetch('/sessions');
      const data = await resp.json();
      currentSessionId = data.current || '';
      var sessions = data.sessions || [];

      sessionSelect.innerHTML = '';
      sessions.forEach(function(s) {
        var opt = document.createElement('option');
        opt.value = s.id;
        opt.textContent = s.title || 'New Chat';
        if (s.id === currentSessionId) opt.selected = true;
        sessionSelect.appendChild(opt);
      });
    } catch (err) {
      console.error('loadSessions error:', err);
    }
  }

  async function switchSession(id) {
    if (id === currentSessionId || !id) return;
    try {
      var resp = await fetch('/sessions/' + id, { method: 'POST' });
      var data = await resp.json();
      currentSessionId = id;
      clearMessages();
      (data.messages || []).forEach(function(m) {
        appendChatMessage(m.role, m.content);
      });
    } catch (err) {
      console.error('switchSession error:', err);
    }
  }

  async function createSession() {
    try {
      var resp = await fetch('/sessions', { method: 'POST' });
      var data = await resp.json();
      currentSessionId = data.id;
      clearMessages();
      await loadSessions();
      input.focus();
    } catch (err) {
      console.error('createSession error:', err);
    }
  }

  async function deleteSession() {
    if (!currentSessionId) return;
    try {
      await fetch('/sessions/' + currentSessionId, { method: 'DELETE' });
      await loadSessions();
      // After deletion, backend auto-switches; reload the new current session.
      if (currentSessionId) {
        await switchSession(currentSessionId);
      } else {
        clearMessages();
      }
      // Reload sessions to get the updated current.
      await loadSessions();
      // Switch to whatever is now current.
      var selected = sessionSelect.value;
      if (selected && selected !== currentSessionId) {
        await switchSession(selected);
      }
    } catch (err) {
      console.error('deleteSession error:', err);
    }
  }

  function clearMessages() {
    messages.innerHTML = '<div class="msg msg-system">Ask your agent anything about mining status, strategy, or give instructions.</div>';
  }

  // Event listeners.
  sendBtn.addEventListener('click', sendMessage);
  input.addEventListener('keydown', function(e) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  });

  // Command bar: quick actions + social.
  document.getElementById('cmd-bar').addEventListener('click', function(e) {
    var el = e.target.closest('a[data-msg]');
    if (el) { sendQuick(el.getAttribute('data-msg')); return; }
    var socialEl = e.target.closest('a[data-social]');
    if (socialEl && !socialLoading) {
      var action = socialEl.getAttribute('data-social');
      if (action === 'post') handleSocialPost();
      else fetchSocial(action);
    }
  });

  // Session controls.
  sessionSelect.addEventListener('change', function() {
    switchSession(sessionSelect.value);
  });
  newChatBtn.addEventListener('click', createSession);
  delChatBtn.addEventListener('click', deleteSession);

  // ── Social ──
  let socialLoading = false;
  let postCooldownUntil = 0;
  let postCooldownTimer = null;

  function setSocialLoading(loading) {
    socialLoading = loading;
    document.querySelectorAll('.cmd-bar a.cmd-social').forEach(function(a) {
      // Don't re-enable Post button if it's in cooldown.
      if (a.getAttribute('data-social') === 'post' && postCooldownUntil > Date.now()) return;
      a.classList.toggle('cmd-disabled', loading);
    });
  }

  function startPostCooldown(retryAfter) {
    postCooldownUntil = Date.now() + retryAfter * 1000;
    updatePostButton();
    if (postCooldownTimer) clearInterval(postCooldownTimer);
    postCooldownTimer = setInterval(function() {
      var remaining = Math.ceil((postCooldownUntil - Date.now()) / 1000);
      if (remaining <= 0) {
        clearInterval(postCooldownTimer);
        postCooldownTimer = null;
        postCooldownUntil = 0;
        updatePostButton();
      } else {
        updatePostButton();
      }
    }, 1000);
  }

  function updatePostButton() {
    var btn = document.querySelector('a[data-social="post"]');
    if (!btn) return;
    var remaining = Math.ceil((postCooldownUntil - Date.now()) / 1000);
    if (remaining > 0) {
      var m = Math.floor(remaining / 60);
      var s = remaining % 60;
      btn.textContent = '/post ' + m + ':' + (s < 10 ? '0' : '') + s;
      btn.classList.add('cmd-disabled');
    } else {
      btn.textContent = '/post';
      btn.classList.remove('cmd-disabled');
    }
  }

  async function fetchSocial(module) {
    setSocialLoading(true);
    var loadingEl = appendChatMessage('loading', 'Loading...');
    try {
      var url = '/social?module=' + module;
      if (module === 'feed') url = '/social?module=moments&feed=friends';
      if (module === 'friends') url = '/social?module=connections';
      var resp = await fetch(url);
      var data = await resp.json();
      if (data.error) {
        loadingEl.className = 'msg msg-system';
        loadingEl.textContent = 'Error: ' + (data.error.message || data.error);
      } else {
        loadingEl.className = 'msg msg-assistant';
        loadingEl.innerHTML = renderSocialResult(module, data);
      }
    } catch (err) {
      loadingEl.className = 'msg msg-system';
      loadingEl.textContent = 'Connection error: ' + err.message;
    }
    setSocialLoading(false);
    messages.scrollTop = messages.scrollHeight;
  }

  function renderSocialResult(module, data) {
    if (module === 'nearby') return renderNearby(data);
    if (module === 'feed') return renderFeed(data);
    if (module === 'friends') return renderFriends(data);
    return '<div class="social-card"><pre>' + escapeHtml(JSON.stringify(data, null, 2)) + '</pre></div>';
  }

  function renderNearby(data) {
    var miners = data.data ? data.data.miners : data.miners;
    if (!miners || miners.length === 0) {
      return '<div class="social-card"><div class="social-card-title">Nearby Miners</div>' +
        '<div class="social-empty">No miners nearby on this token right now.</div></div>';
    }
    var html = '<div class="social-card"><div class="social-card-title">Nearby Miners (' + miners.length + ')</div>';
    miners.forEach(function(m) {
      var avatarHtml = m.avatar_url
        ? '<div class="social-avatar"><img src="' + escapeHtml(m.avatar_url) + '" alt=""></div>'
        : '<div class="social-avatar">' + escapeHtml((m.display_name || '?').charAt(0).toUpperCase()) + '</div>';
      var badge = '';
      if (m.is_friend) badge = ' <span class="social-badge social-badge-friend">friend</span>';
      else if (m.i_follow) badge = ' <span class="social-badge social-badge-following">following</span>';
      html += '<div class="social-item">' + avatarHtml +
        '<span class="social-name">' + escapeHtml(m.display_name || m.agent_id) + '</span>' + badge +
        ' <span class="social-meta">' + (m.inscription_count || 0) + ' inscriptions</span></div>';
    });
    html += '</div>';
    return html;
  }

  function renderFeed(data) {
    var moments = data.data ? data.data.moments : data.moments;
    if (!moments || moments.length === 0) {
      return '<div class="social-card"><div class="social-card-title">Friends Feed</div>' +
        '<div class="social-empty">No moments yet. Make some friends and check back!</div></div>';
    }
    var html = '<div class="social-card"><div class="social-card-title">Friends Feed</div>';
    moments.forEach(function(m) {
      var avatarHtml = m.avatar_url
        ? '<div class="social-avatar"><img src="' + escapeHtml(m.avatar_url) + '" alt=""></div>'
        : '<div class="social-avatar">' + escapeHtml((m.display_name || '?').charAt(0).toUpperCase()) + '</div>';
      var time = m.created_at ? new Date(m.created_at).toLocaleString() : '';
      html += '<div class="moment-item"><div class="moment-header">' + avatarHtml +
        '<span class="social-name">' + escapeHtml(m.display_name || m.agent_id) + '</span>' +
        '<span class="moment-time">' + escapeHtml(time) + '</span>';
      if (m.likes_count > 0) html += ' <span class="moment-likes">\u2665 ' + m.likes_count + '</span>';
      html += '</div><div class="social-content">' + escapeHtml(m.content) + '</div></div>';
    });
    html += '</div>';
    return html;
  }

  function renderFriends(data) {
    var friends = data.data ? data.data.friends : data.friends;
    if (!friends || friends.length === 0) {
      return '<div class="social-card"><div class="social-card-title">Friends</div>' +
        '<div class="social-empty">No friends yet. Follow agents from Nearby to connect!</div></div>';
    }
    var html = '<div class="social-card"><div class="social-card-title">Friends (' + friends.length + ')</div>';
    friends.forEach(function(f) {
      var avatarHtml = f.avatar_url
        ? '<div class="social-avatar"><img src="' + escapeHtml(f.avatar_url) + '" alt=""></div>'
        : '<div class="social-avatar">' + escapeHtml((f.display_name || '?').charAt(0).toUpperCase()) + '</div>';
      html += '<div class="social-item">' + avatarHtml +
        '<span class="social-name">' + escapeHtml(f.display_name || f.agent_id) + '</span>' +
        ' <span class="social-meta">trust ' + (f.trust_score || 0) + '</span></div>';
    });
    html += '</div>';
    return html;
  }

  async function handleSocialPost() {
    // Block if cooldown is active.
    if (postCooldownUntil > Date.now()) {
      var secs = Math.ceil((postCooldownUntil - Date.now()) / 1000);
      var m = Math.floor(secs / 60);
      var s = secs % 60;
      appendChatMessage('system', 'Cooldown active — wait ' + m + ':' + (s < 10 ? '0' : '') + s + ' before posting again.');
      return;
    }

    setSocialLoading(true);
    var loadingEl = appendChatMessage('loading', 'Agent is writing a moment...');
    try {
      var resp = await fetch('/social/moment', { method: 'POST' });
      var data = await resp.json();
      // Handle cooldown (from server-side cache or upstream API).
      if (data.cooldown && data.retry_after) {
        startPostCooldown(data.retry_after);
      }
      if (data.error) {
        loadingEl.className = 'msg msg-system';
        loadingEl.textContent = 'Post failed: ' + (data.error.message || data.error);
      } else if (data.cooldown && !data.response) {
        // Server returned cooldown before even calling LLM.
        var secs = data.retry_after || 0;
        var m = Math.floor(secs / 60);
        var s = secs % 60;
        loadingEl.className = 'msg msg-system';
        loadingEl.textContent = 'Cooldown active — wait ' + m + ':' + (s < 10 ? '0' : '') + s + ' before posting again.';
      } else if (data.content) {
        loadingEl.className = 'msg msg-assistant';
        loadingEl.innerHTML = '<span class="msg-role">Agent:</span>' +
          '<div class="social-card"><div class="social-card-title">Moment Posted</div>' +
          '<div class="social-content">' + escapeHtml(data.content) + '</div></div>';
      }
    } catch (err) {
      loadingEl.className = 'msg msg-system';
      loadingEl.textContent = 'Connection error: ' + err.message;
    }
    setSocialLoading(false);
    messages.scrollTop = messages.scrollHeight;
  }

  // Init.
  connectSSE();
  updateFooter();
  loadSessions().then(function() {
    // Load current session messages on page open.
    if (currentSessionId) {
      fetch('/sessions/' + currentSessionId, { method: 'POST' })
        .then(function(r) { return r.json(); })
        .then(function(data) {
          if (data.messages && data.messages.length > 0) {
            clearMessages();
            data.messages.forEach(function(m) {
              appendChatMessage(m.role, m.content);
            });
          }
        })
        .catch(function() {});
    }
  });
  input.focus();
})();
