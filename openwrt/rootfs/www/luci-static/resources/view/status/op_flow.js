'use strict';
'require view';
'require rpc';
'require dom';
'require ui';

var callDashboard = rpc.declare({
	object: 'op-flow',
	method: 'dashboard',
	expect: {}
});

var callUpdate = rpc.declare({
	object: 'op-flow',
	method: 'update',
	expect: {}
});

var callHistory = rpc.declare({
	object: 'op-flow',
	method: 'history',
	params: [ 'granularity', 'period' ],
	expect: {}
});

var callExport = rpc.declare({
	object: 'op-flow',
	method: 'export',
	params: [ 'granularity', 'period' ],
	expect: {}
});

function ensureStyles() {
	var critical = document.getElementById('op-flow-insight-critical-style');
	if (!critical) {
		critical = document.createElement('style');
		critical.id = 'op-flow-insight-critical-style';
		critical.textContent =
			'.ofi-root{clear:both!important;display:block!important;float:none!important;' +
				'max-width:100%!important;min-width:0!important;width:100%!important}' +
			'.ofi-root .ofi-panel{float:none!important;max-width:100%!important;' +
				'position:relative!important;width:100%!important}' +
			'.ofi-root .ofi-table-scroll{display:block!important;max-width:100%!important;' +
				'overflow-x:auto!important;width:100%!important}';
		document.head.appendChild(critical);
	}

	var stylesheet = document.getElementById('op-flow-insight-stylesheet');
	if (!stylesheet) {
		stylesheet = document.createElement('link');
		stylesheet.id = 'op-flow-insight-stylesheet';
		stylesheet.rel = 'stylesheet';
		stylesheet.type = 'text/css';
		stylesheet.href = L.resource('op-flow.css') + '?v=0.1.1-r9';
		document.head.appendChild(stylesheet);
	}
	return stylesheet;
}

function bytes(value) {
	var n = Number(value || 0);
	var units = [ 'B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB' ];
	var i = 0;
	while (n >= 1024 && i < units.length - 1) {
		n /= 1024;
		i++;
	}
	return (i === 0 ? n.toFixed(0) : n.toFixed(n >= 100 ? 0 : n >= 10 ? 1 : 2)) + ' ' + units[i];
}

function rate(value) {
	return bytes(value) + '/s';
}

function text(value, fallback) {
	return value === undefined || value === null || value === '' ? (fallback || '—') : String(value);
}

function riskClass(score) {
	if (score >= 80) return 'critical';
	if (score >= 60) return 'high';
	if (score >= 40) return 'medium';
	if (score >= 20) return 'guarded';
	return 'low';
}

function riskBadge(risk) {
	risk = risk || { score: 0, evidence: [] };
	var evidence = (risk.evidence || []).map(function(item) {
		return item.source + (item.detail ? ' · ' + item.detail : '');
	}).join('\n');
	var title = risk.score === 0
		? _('Not found in the loaded datasets; this does not mean the IP is known to be safe')
		: evidence;
	return E('span', {
		'class': 'ofi-risk ofi-risk-' + riskClass(Number(risk.score || 0)),
		'title': title
	}, String(risk.score || 0));
}

function endpoint(value) {
	if (!value) return '—';
	var ip = text(value.ip);
	if (ip.indexOf(':') >= 0) ip = '[' + ip + ']';
	return ip + (value.port ? ':' + value.port : '');
}

function country(value) {
	var code = text(value);
	if (code === '—' || code === 'LAN') return code === 'LAN' ? _('LAN') : code;
	try {
		if (typeof Intl !== 'undefined' && Intl.DisplayNames) {
			var language = document.documentElement.lang ||
				(typeof navigator !== 'undefined' && navigator.language) || 'en';
			var name = new Intl.DisplayNames([ language ], { type: 'region' }).of(code);
			if (name && name !== code) return name + ' (' + code + ')';
		}
	} catch (e) {}
	return code;
}

function svgNode(name, attributes, children) {
	var node = document.createElementNS('http://www.w3.org/2000/svg', name);
	Object.keys(attributes || {}).forEach(function(key) {
		node.setAttribute(key, String(attributes[key]));
	});
	(children || []).forEach(function(child) {
		node.appendChild(child);
	});
	return node;
}

function timeLabel(value, fallback) {
	var date = value ? new Date(value) : null;
	if (!date || isNaN(date.getTime())) return fallback;
	return date.toLocaleTimeString([], {
		hour: '2-digit',
		minute: '2-digit',
		second: '2-digit'
	});
}

function warningText(value) {
	value = String(value || '');
	var dynamic = [
		[
			'Cumulative state file is damaged; restarted from current connections: ',
			_('Cumulative state file is damaged; restarted from current connections: ')
		],
		[
			'Unable to read conntrack: ',
			_('Unable to read conntrack: ')
		],
		[
			'Unable to detect LAN prefixes through ubus; using configured prefixes: ',
			_('Unable to detect LAN prefixes through ubus; using configured prefixes: ')
		],
		[
			'Conntrack destroy events are unavailable; very short connections may be undercounted: ',
			_('Conntrack destroy events are unavailable; very short connections may be undercounted: ')
		],
		[
			'Failed to save cumulative state: ',
			_('Failed to save cumulative state: ')
		]
	];
	for (var i = 0; i < dynamic.length; i++) {
		if (value.indexOf(dynamic[i][0]) === 0)
			return dynamic[i][1] + value.slice(dynamic[i][0].length);
	}
	return _(value);
}

function sparkline(points) {
	points = points || [];
	if (points.length < 2) {
		return E('div', { 'class': 'ofi-chart-empty' },
			_('The trend appears after several samples are collected'));
	}
	var max = 1;
	points.forEach(function(p) {
		max = Math.max(max, Number(p.upload_bps || 0), Number(p.download_bps || 0));
	});
	function path(key) {
		return points.map(function(p, index) {
			var x = (index / (points.length - 1)) * 100;
			var y = 38 - (Number(p[key] || 0) / max) * 34;
			return (index ? 'L' : 'M') + x.toFixed(2) + ',' + y.toFixed(2);
		}).join(' ');
	}
	var middle = points[Math.floor((points.length - 1) / 2)];
	var startLabel = timeLabel(points[0].at, _('About 10 minutes ago'));
	var middleLabel = timeLabel(middle.at, _('About 5 minutes ago'));
	var endLabel = timeLabel(points[points.length - 1].at, _('Now'));
	return E('div', { 'class': 'ofi-chart-wrap' }, [
		E('div', { 'class': 'ofi-chart-stage' }, [
			E('div', { 'class': 'ofi-axis-title ofi-axis-title-y' }, _('Rate')),
			E('div', { 'class': 'ofi-y-scale', 'aria-hidden': 'true' }, [
				E('span', {}, rate(max)),
				E('span', {}, rate(max / 2)),
				E('span', {}, '0 B/s')
			]),
			E('div', { 'class': 'ofi-plot' }, [
				svgNode('svg', {
					'class': 'ofi-chart',
					'viewBox': '0 0 100 40',
					'preserveAspectRatio': 'none',
					'role': 'img',
					'aria-label': _('Live upload and download bandwidth trend with time on the horizontal axis and rate on the vertical axis')
				}, [
					svgNode('path', {
						'class': 'ofi-grid',
						'd': 'M0,38 L100,38 M0,20 L100,20 M0,2 L100,2'
					}),
					svgNode('path', {
						'class': 'ofi-line ofi-line-down',
						'd': path('download_bps')
					}),
					svgNode('path', {
						'class': 'ofi-line ofi-line-up',
						'd': path('upload_bps')
					})
				]),
				E('div', { 'class': 'ofi-x-scale', 'aria-hidden': 'true' }, [
					E('span', {}, startLabel),
					E('span', {}, middleLabel),
					E('span', {}, endLabel)
				])
			])
		]),
		E('div', { 'class': 'ofi-axis-title ofi-axis-title-x' }, _('Time')),
		E('div', { 'class': 'ofi-legend' }, [
			E('span', { 'class': 'ofi-key ofi-key-down' }, _('Download')),
			E('span', { 'class': 'ofi-key ofi-key-up' }, _('Upload')),
			E('span', { 'class': 'ofi-chart-max' }, _('Peak') + ' ' + rate(max))
		])
	]);
}

function compareHostIP(left, right) {
	left = String(left || '');
	right = String(right || '');
	var left4 = left.split('.');
	var right4 = right.split('.');
	if (left4.length === 4 && right4.length === 4) {
		for (var i = 0; i < 4; i++) {
			var difference = Number(left4[i]) - Number(right4[i]);
			if (difference) return difference;
		}
		return 0;
	}
	if (left4.length === 4) return -1;
	if (right4.length === 4) return 1;
	try {
		return left.localeCompare(right, undefined, { numeric: true, sensitivity: 'base' });
	} catch (e) {
		return left < right ? -1 : left > right ? 1 : 0;
	}
}

function orderedHosts(hosts) {
	return (hosts || []).slice().sort(function(left, right) {
		return compareHostIP(left.ip, right.ip);
	});
}

function hostKey(host) {
	return host && (host.id || ('ip:' + host.ip));
}

function hostAddresses(host) {
	if (host && host.addresses && host.addresses.length)
		return host.addresses;
	if (host && host.ip) {
		return [ {
			ip: host.ip,
			family: host.ip.indexOf(':') >= 0 ? 'ipv6' : 'ipv4',
			scope: 'lan'
		} ];
	}
	return [];
}

function addressScopeLabel(address, router) {
	if (address.family === 'ipv4')
		return router ? _('Router LAN IPv4') : _('IPv4');
	if (router)
		return _('Router LAN IPv6');
	if (address.scope === 'link-local')
		return _('Link-local IPv6');
	if (address.scope === 'lan')
		return _('LAN IPv6');
	return _('Global IPv6');
}

function addressBadge(address, router) {
	return E('span', {
		'class': 'ofi-address ofi-address-' + text(address.family, 'ipv4') +
			' ofi-address-' + text(address.scope, 'global'),
		'title': addressScopeLabel(address, router)
	}, [
		E('span', { 'class': 'ofi-address-kind' },
			addressScopeLabel(address, router)),
		E('span', { 'class': 'ofi-mono' }, text(address.ip))
	]);
}

function hostLabel(host) {
	return text(host && host.hostname, _('Unnamed host')) + ' · ' +
		text(host && host.ip);
}

function hostRows(hosts, selectedHostID, onSelect) {
	if (!hosts || !hosts.length) {
		return [ E('tr', { 'class': 'tr' }, [
			E('td', { 'colspan': 7, 'class': 'td ofi-empty' },
			_('No LAN hosts are currently online')) ]) ];
	}
	return hosts.map(function(host) {
		var id = hostKey(host);
		var activate = function(event) {
			if (event && event.type === 'keydown' &&
				event.key !== 'Enter' && event.key !== ' ') return;
			if (event) {
				event.preventDefault();
				event.stopPropagation();
			}
			onSelect(host);
		};
		return E('tr', {
			'class': 'tr ofi-host-row' +
				(id === selectedHostID ? ' ofi-host-selected' : ''),
			'data-host': id,
			'data-ip': host.ip,
			'tabindex': '0',
			'role': 'button',
			'aria-label': _('View current connections for') + ' ' + hostLabel(host),
			'aria-selected': id === selectedHostID ? 'true' : 'false',
			'title': _('Click to view current connections for this host'),
			'click': activate,
			'keydown': activate
		}, [
			E('td', { 'class': 'td' }, [
				E('strong', {}, text(host.hostname, _('Unnamed host'))),
				E('div', { 'class': 'ofi-address-list' },
					hostAddresses(host).map(function(address) {
						return addressBadge(address, false);
					})),
				host.mac ? E('div', { 'class': 'ofi-mono ofi-subtle' }, host.mac) : ''
			]),
			E('td', { 'class': 'td ofi-num ofi-down' }, rate(host.download_bps)),
			E('td', { 'class': 'td ofi-num ofi-up' }, rate(host.upload_bps)),
			E('td', { 'class': 'td ofi-num' }, bytes(host.downloaded)),
			E('td', { 'class': 'td ofi-num' }, bytes(host.uploaded)),
			E('td', { 'class': 'td ofi-num' }, String(host.active_flows || 0)),
			E('td', { 'class': 'td ofi-num' }, riskBadge({ score: host.max_risk || 0 }))
		]);
	});
}

function flowRows(flows, emptyMessage) {
	if (!flows || !flows.length) {
		return [ E('tr', { 'class': 'tr' }, [
			E('td', { 'colspan': 8, 'class': 'td ofi-empty' },
				emptyMessage || _('There are no active connections to display'))
		]) ];
	}
	return flows.map(function(flow) {
		var geo = flow.geo || {};
		var place = country(geo.country_code);
		var asn = geo.asn ? 'AS' + geo.asn + ' · ' + text(geo.asn_org) : text(geo.asn_org);
		return E('tr', { 'class': 'tr', 'data-host': flow.host_ip || '' }, [
			E('td', { 'class': 'td' }, [
				E('span', { 'class': 'ofi-direction ofi-direction-' + flow.direction },
					flow.direction === 'inbound' ? _('Inbound') : _('Outbound')),
				E('span', { 'class': 'ofi-proto' }, text(flow.protocol).toUpperCase())
			]),
			E('td', { 'class': 'td ofi-mono' }, endpoint(flow.source)),
			E('td', { 'class': 'td ofi-arrow' }, '→'),
			E('td', { 'class': 'td ofi-mono' }, endpoint(flow.destination)),
			E('td', { 'class': 'td' }, [
				E('div', {}, place),
				E('div', { 'class': 'ofi-subtle', 'title': asn }, asn)
			]),
			E('td', { 'class': 'td ofi-num ofi-down' }, rate(flow.download_bps)),
			E('td', { 'class': 'td ofi-num ofi-up' }, rate(flow.upload_bps)),
			E('td', { 'class': 'td ofi-num' }, riskBadge(flow.risk))
		]);
	});
}

function addressText(addresses, family) {
	return (addresses || []).filter(function(address) {
		return address.family === family;
	}).map(function(address) {
		var suffix = family === 'ipv6'
			? ' (' + addressScopeLabel(address, false) + ')'
			: '';
		return address.ip + suffix;
	}).join('\n') || '—';
}

function usageRows(records) {
	if (!records || !records.length) {
		return [ E('tr', { 'class': 'tr' }, [
			E('td', { 'colspan': 6, 'class': 'td ofi-empty' },
				_('No retained traffic records exist for this period'))
		]) ];
	}
	return records.map(function(record) {
		return E('tr', { 'class': 'tr' }, [
			E('td', { 'class': 'td' }, [
				E('strong', {}, text(record.hostname, _('Unnamed host'))),
				record.mac ? E('div', {
					'class': 'ofi-mono ofi-subtle'
				}, record.mac) : ''
			]),
			E('td', {
				'class': 'td ofi-mono ofi-address-cell',
				'title': addressText(record.addresses, 'ipv4')
			}, addressText(record.addresses, 'ipv4')),
			E('td', {
				'class': 'td ofi-mono ofi-address-cell',
				'title': addressText(record.addresses, 'ipv6')
			}, addressText(record.addresses, 'ipv6')),
			E('td', { 'class': 'td ofi-num ofi-down' },
				bytes(record.downloaded)),
			E('td', { 'class': 'td ofi-num ofi-up' },
				bytes(record.uploaded)),
			E('td', { 'class': 'td ofi-num' },
				bytes(Number(record.downloaded || 0) + Number(record.uploaded || 0)))
		]);
	});
}

function periodKindLabel(kind) {
	switch (kind) {
	case 'day': return _('Day');
	case 'quarter': return _('Quarter');
	case 'year': return _('Year');
	default: return _('Month');
	}
}

function saveTXT(result) {
	var blob = new Blob([ text(result.content, '') ], {
		type: 'text/plain;charset=utf-8'
	});
	var url = URL.createObjectURL(blob);
	var link = document.createElement('a');
	link.href = url;
	link.download = text(result.filename, 'op-flow-traffic.txt');
	link.style.display = 'none';
	document.body.appendChild(link);
	link.click();
	document.body.removeChild(link);
	window.setTimeout(function() { URL.revokeObjectURL(url); }, 1000);
}

function subtabButtons(items, current, onSelect, className) {
	return E('div', {
		'class': 'ofi-subtabs ' + (className || ''),
		'role': 'tablist'
	}, items.map(function(item) {
		return E('button', {
			'class': 'cbi-button ofi-subtab' +
				(item.value === current ? ' ofi-subtab-active' : ''),
			'type': 'button',
			'role': 'tab',
			'aria-selected': item.value === current ? 'true' : 'false',
			'click': function(event) {
				event.preventDefault();
				onSelect(item.value);
			}
		}, item.label + (item.count === undefined ? '' : ' (' + item.count + ')'));
	}));
}

function table(head, body, className) {
	return E('div', { 'class': 'ofi-table-scroll' }, [
		E('table', { 'class': 'table ofi-table ' + (className || '') }, [
			E('thead', { 'class': 'thead' }, [
				E('tr', { 'class': 'tr table-titles' },
					head.map(function(item) { return E('th', { 'class': 'th' }, item); }))
			]),
			E('tbody', {}, body)
		])
	]);
}

function darkThemeActive() {
	var elements = [
		document.body,
		document.querySelector('.main-right'),
		document.documentElement
	];

	for (var i = 0; i < elements.length; i++) {
		if (!elements[i]) continue;
		var value = window.getComputedStyle(elements[i]).backgroundColor;
		var match = value && value.match(/^rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/);
		if (!match) continue;
		var red = Number(match[1]);
		var green = Number(match[2]);
		var blue = Number(match[3]);
		var luminance = (red * 299 + green * 587 + blue * 114) / 1000;
		if (luminance < 128) return true;
		if (luminance >= 200) return false;
	}

	return window.matchMedia &&
		window.matchMedia('(prefers-color-scheme: dark)').matches;
}
return view.extend({
	load: function() {
		return callDashboard().catch(function(err) {
			return { error: err.message || String(err) };
		});
	},

	render: function(data) {
		ensureStyles();
		this.root = E('div', {
			'class': 'cbi-map ofi-root' + (darkThemeActive() ? ' ofi-dark' : '')
		});
		this.activeTab = this.activeTab || 'trend';
		this.selectedHostID = this.selectedHostID || '';
		this.connectionFamily = this.connectionFamily || 'ipv4';
		this.historyGranularity = this.historyGranularity || 'month';
		this.historyPeriod = this.historyPeriod || '';
		this.historyRequestSerial = this.historyRequestSerial || 0;
		this.renderData(data);
		L.Poll.add(L.bind(function() {
			return callDashboard().then(L.bind(this.renderData, this)).catch(function() {});
		}, this), 2);
		return this.root;
	},

	requestHistory: function(granularity, period) {
		var options = ((this.lastData && this.lastData.usage_periods) || {})[
			granularity
		] || [];
		period = period || options[0] || '';
		if (!period) return Promise.resolve();
		this.historyGranularity = granularity;
		this.historyPeriod = period;
		this.historyLoading = true;
		var serial = ++this.historyRequestSerial;
		if (this.lastData) this.renderData(this.lastData);
		return callHistory(granularity, period).then(L.bind(function(result) {
			if (serial !== this.historyRequestSerial) return;
			this.historyData = result;
			this.historyLoading = false;
			if (this.lastData) this.renderData(this.lastData);
		}, this)).catch(L.bind(function(err) {
			if (serial !== this.historyRequestSerial) return;
			this.historyLoading = false;
			ui.addNotification(null, E('p', {},
				_('Unable to load retained traffic history:') + ' ' +
				text(err && err.message, String(err))));
			if (this.lastData) this.renderData(this.lastData);
		}, this));
	},

	renderData: function(data) {
		if (!this.root) return;
		this.lastData = data;
		this.root.classList.toggle('ofi-dark', darkThemeActive());
		if (!data || data.error) {
			dom.content(this.root, [
				E('h2', {}, _('Flow Insight')),
				E('div', { 'class': 'alert-message error' },
					_('Unable to connect to the backend service:') + ' ' +
					text(data && data.error, _('Make sure the op-flow service is running')))
			]);
			return;
		}
		var totals = data.totals || {};
		var health = data.health || {};
		var dataStatus = data.data || {};
		var hosts = orderedHosts(data.hosts);
		var selectedHost = null;
		for (var hostIndex = 0; hostIndex < hosts.length; hostIndex++) {
			if (hostKey(hosts[hostIndex]) === this.selectedHostID) {
				selectedHost = hosts[hostIndex];
				break;
			}
		}
		if (!selectedHost && this.selectedHostID) {
			this.selectedHostID = '';
			if (this.activeTab === 'connections') this.activeTab = 'hosts';
		}
		var periodOptions = (data.usage_periods || {})[this.historyGranularity] || [];
		if (!this.historyPeriod || periodOptions.indexOf(this.historyPeriod) < 0)
			this.historyPeriod = periodOptions[0] || '';
		var selectedFlows = selectedHost ? (data.flows || []).filter(function(flow) {
			if (flow.host_id && flow.host_id === hostKey(selectedHost)) return true;
			return hostAddresses(selectedHost).some(function(address) {
				return flow.host_ip === address.ip;
			});
		}) : [];
		var familyFlows = {
			ipv4: selectedFlows.filter(function(flow) {
				return (flow.ip_version || (flow.host_ip.indexOf(':') >= 0
					? 'ipv6' : 'ipv4')) === 'ipv4';
			}),
			ipv6: selectedFlows.filter(function(flow) {
				return (flow.ip_version || (flow.host_ip.indexOf(':') >= 0
					? 'ipv6' : 'ipv4')) === 'ipv6';
			})
		};
		var self = this;
		var selectHost = function(host) {
			self.selectedHostID = hostKey(host);
			self.connectionFamily = hostAddresses(host).some(function(address) {
				return address.family === 'ipv4';
			}) ? 'ipv4' : 'ipv6';
			self.activeTab = 'connections';
			self.pendingDetailFocus = true;
			self.renderData(self.lastData);
		};
		var warnings = (health.warnings || []).map(function(item) {
			return E('div', { 'class': 'alert-message warning' }, warningText(item));
		});
		if (!dataStatus.loaded) {
			warnings.push(E('div', { 'class': 'alert-message warning' },
				_('The offline attribution and risk database is not loaded. Click "Update datasets"; traffic accounting continues to work during the update.')));
		}
		var updated = dataStatus.updated_at
			? new Date(dataStatus.updated_at).toLocaleString()
			: _('Never updated');
		var trendPanel = E('section', {
			'class': 'cbi-section ofi-panel',
			'data-tab': 'trend',
			'data-tab-title': _('Live trend'),
			'data-tab-active': this.activeTab === 'trend' ? 'true' : null,
			'data-ofi-panel': 'trend'
		}, [
			E('h3', { 'class': 'ofi-panel-heading' }, [
				E('span', {}, _('Live bandwidth trend')),
				E('small', { 'class': 'ofi-subtle' }, _('About the last 10 minutes'))
			]),
			sparkline(data.history)
		]);
		var hostsPanel = E('section', {
			'class': 'cbi-section ofi-panel',
			'data-tab': 'hosts',
			'data-tab-title': _('LAN hosts'),
			'data-tab-active': this.activeTab === 'hosts' ? 'true' : null,
			'data-ofi-panel': 'hosts'
		}, [
			E('h3', { 'class': 'ofi-panel-heading' }, [
				E('span', {}, _('LAN hosts')),
				E('small', { 'class': 'ofi-subtle' },
					String(hosts.length) + ' ' + _('hosts recorded'))
			]),
			E('div', { 'class': 'cbi-section-descr ofi-panel-hint' },
				_('Only online hosts are shown. Records are retained while a host is offline; click a row to view IPv4 or IPv6 connections.')),
			table(
				[
					_('Host'), _('Live download'), _('Live upload'),
					_('This month downloaded'), _('This month uploaded'),
					_('Connections'), _('Risk')
				],
				hostRows(hosts, this.selectedHostID, selectHost),
				'ofi-host-table'
			)
		]);
		var connectionsContent;
		if (selectedHost) {
			connectionsContent = [
				E('h3', { 'class': 'ofi-panel-heading' }, [
					E('span', {}, _('Current connections') + ' · ' +
						text(selectedHost.hostname, _('Unnamed host'))),
					E('small', { 'class': 'ofi-subtle' },
						String(selectedFlows.length) + ' ' + _('active connections'))
				]),
				E('div', { 'class': 'ofi-detail-addresses' }, [
					E('div', { 'class': 'ofi-address-list' },
						hostAddresses(selectedHost).map(function(address) {
							return addressBadge(address, false);
						})),
					selectedHost.mac ? E('span', {
						'class': 'ofi-mono ofi-subtle'
					}, selectedHost.mac) : ''
				]),
				E('div', { 'class': 'ofi-detail-summary' }, [
					E('span', {}, [ _('Live download') + ' ',
						E('strong', { 'class': 'ofi-down' }, rate(selectedHost.download_bps)) ]),
					E('span', {}, [ _('Live upload') + ' ',
						E('strong', { 'class': 'ofi-up' }, rate(selectedHost.upload_bps)) ]),
					E('span', {}, _('This month downloaded') + ' ' +
						bytes(selectedHost.downloaded)),
					E('span', {}, _('This month uploaded') + ' ' +
						bytes(selectedHost.uploaded))
				]),
				subtabButtons([
					{ value: 'ipv4', label: _('IPv4'), count: familyFlows.ipv4.length },
					{ value: 'ipv6', label: _('IPv6'), count: familyFlows.ipv6.length }
				], this.connectionFamily, function(family) {
					self.connectionFamily = family;
					self.renderData(self.lastData);
				}, 'ofi-family-tabs'),
				table(
					[
						_('Direction'), _('Source IP'), '', _('Destination IP'),
						_('Attribution / ASN'), _('Download'), _('Upload'), _('Risk')
					],
					flowRows(familyFlows[this.connectionFamily],
						this.connectionFamily === 'ipv6'
							? _('This host has no active IPv6 connections')
							: _('This host has no active IPv4 connections')),
					'ofi-flow-table'
				)
			];
		} else {
			connectionsContent = [
				E('h3', {}, _('Current connections')),
				E('div', { 'class': 'cbi-section-descr ofi-connection-placeholder' },
					_('Select a LAN host to view its current connections.'))
			];
		}
		var connectionsPanel = E('section', {
			'class': 'cbi-section ofi-panel',
			'data-tab': 'connections',
			'data-tab-title': _('Current connections'),
			'data-tab-active': this.activeTab === 'connections' ? 'true' : null,
			'tabindex': '-1',
			'data-ofi-panel': 'connections'
		}, connectionsContent);
		var historyMatches = this.historyData &&
			this.historyData.granularity === this.historyGranularity &&
			this.historyData.period === this.historyPeriod;
		var displayedHistory = historyMatches ? this.historyData : null;
		var historyTotals = (displayedHistory && displayedHistory.totals) || {};
		var historyPanel = E('section', {
			'class': 'cbi-section ofi-panel',
			'data-tab': 'history',
			'data-tab-title': _('Traffic history'),
			'data-tab-active': this.activeTab === 'history' ? 'true' : null,
			'data-ofi-panel': 'history'
		}, [
			E('h3', { 'class': 'ofi-panel-heading' }, [
				E('span', {}, _('Retained traffic history')),
				E('small', { 'class': 'ofi-subtle' },
					this.historyPeriod || _('No period available'))
			]),
			E('div', { 'class': 'ofi-history-controls' }, [
				subtabButtons([
					{ value: 'day', label: _('Day') },
					{ value: 'month', label: _('Month') },
					{ value: 'quarter', label: _('Quarter') },
					{ value: 'year', label: _('Year') }
				], this.historyGranularity, function(granularity) {
					var options = (self.lastData.usage_periods || {})[granularity] || [];
					self.requestHistory(granularity, options[0] || '');
				}, 'ofi-period-tabs'),
				E('label', { 'class': 'ofi-period-picker' }, [
					E('span', {}, _('Select period')),
					E('select', {
						'class': 'cbi-input-select',
						'disabled': this.historyLoading ? '' : null,
						'change': function(event) {
							self.requestHistory(self.historyGranularity,
								event.target.value);
						}
					}, periodOptions.map(function(period) {
						return E('option', {
							'value': period,
							'selected': period === self.historyPeriod ? '' : null
						}, period);
					}))
				]),
				E('button', {
					'class': 'cbi-button cbi-button-action',
					'type': 'button',
					'disabled': this.historyLoading || !this.historyPeriod ? '' : null,
					'click': ui.createHandlerFn(this, function() {
						return callExport(self.historyGranularity,
							self.historyPeriod).then(function(result) {
							saveTXT(result);
						});
					})
				}, _('Export TXT'))
			]),
			E('div', { 'class': 'ofi-detail-summary' }, [
				E('span', {}, periodKindLabel(this.historyGranularity) + ' ' +
					text(this.historyPeriod)),
				E('span', {}, [ _('Downloaded') + ' ',
					E('strong', { 'class': 'ofi-down' },
						bytes(historyTotals.downloaded)) ]),
				E('span', {}, [ _('Uploaded') + ' ',
					E('strong', { 'class': 'ofi-up' },
						bytes(historyTotals.uploaded)) ])
			]),
			this.historyLoading
				? E('div', { 'class': 'ofi-chart-empty' },
					_('Loading retained traffic history…'))
				: table(
					[
						_('Host'), _('IPv4'), _('IPv6'), _('Downloaded'),
						_('Uploaded'), _('Total')
					],
					usageRows((displayedHistory && displayedHistory.records) || []),
					'ofi-usage-table'
				)
		]);
		[
			[ 'trend', trendPanel ],
			[ 'hosts', hostsPanel ],
			[ 'connections', connectionsPanel ],
			[ 'history', historyPanel ]
		].forEach(function(entry) {
			entry[1].addEventListener('cbi-tab-active', function() {
				self.activeTab = entry[0];
				if (entry[0] === 'history' && !self.historyLoading &&
					(!self.historyData ||
					self.historyData.granularity !== self.historyGranularity ||
					self.historyData.period !== self.historyPeriod)) {
					self.requestHistory(self.historyGranularity, self.historyPeriod);
				}
			});
		});
		var tabGroup = E('div', { 'class': 'ofi-tab-group' }, [
			trendPanel, hostsPanel, connectionsPanel, historyPanel
		]);
		var resetAt = totals.next_reset_at
			? new Date(totals.next_reset_at).toLocaleString()
			: '—';
		var routerAddresses = health.router_lan_addresses || [];
		var content = [
			E('h2', {}, _('Flow Insight')),
			E('p', { 'class': 'ofi-lead' },
				_('Cumulative LAN-host usage, live connections, and offline IP risk evidence')),
			E('div', { 'class': 'ofi-toolbar' }, [
				E('span', { 'class': 'ofi-live' }, [
					E('span', { 'class': 'ofi-live-dot' }), _('Refreshes every 2 seconds')
				]),
				E('button', {
					'class': 'cbi-button cbi-button-action',
					'disabled': dataStatus.update_running ? '' : null,
					'click': ui.createHandlerFn(this, function() {
						return callUpdate().then(function() {
							ui.addNotification(null, E('p', {},
								_('Dataset update started in the background.')));
						});
					})
				}, dataStatus.update_running ? _('Updating…') : _('Update datasets'))
			])
		].concat(warnings, [
			E('section', { 'class': 'cbi-section ofi-summary-section' }, [
				E('h3', {}, _('Traffic overview')),
				table(
					[
						_('Current download'), _('Current upload'),
						_('This month downloaded'), _('This month uploaded'),
						_('Active hosts / connections'), _('Current highest risk')
					],
					[
						E('tr', { 'class': 'tr' }, [
							E('td', { 'class': 'td ofi-num ofi-down' },
								rate(totals.download_bps)),
							E('td', { 'class': 'td ofi-num ofi-up' },
								rate(totals.upload_bps)),
							E('td', { 'class': 'td ofi-num' }, bytes(totals.downloaded)),
							E('td', { 'class': 'td ofi-num' }, bytes(totals.uploaded)),
							E('td', { 'class': 'td ofi-num' },
								(totals.active_hosts || 0) + ' / ' +
								(totals.active_flows || 0)),
							E('td', { 'class': 'td ofi-num' },
								riskBadge({ score: totals.highest_risk || 0 }))
						])
					],
					'ofi-summary-table'
				)
			]),
			tabGroup,
			E('div', { 'class': 'alert-message notice ofi-footnote' }, [
				E('strong', {}, _('Current accounting period:') + ' '),
				text(totals.period) + '. ' +
					_('The live cumulative counters reset at local router time on:') +
					' ' + resetAt,
				E('br'),
				E('strong', {}, _('Risk score:') + ' '),
				_('A score of 0 means the IP was not observed in the loaded datasets; it does not mean safe. The score starts with the most severe evidence, adds 5 for each additional independent source, and is capped at 100. It is only a triage aid and never blocks an IP automatically.'),
				E('br'),
				_('Data updated:') + ' ' + updated + '. ' +
				_('Attribution is limited to country/region and ASN, and all lookups run locally on the router.'),
				E('br'),
				_('Monitored LAN prefixes:') + ' ' +
				((health.lan_prefixes || []).join(', ') || '—'),
				E('br'),
				E('span', {}, _('Router LAN addresses:') + ' '),
				routerAddresses.length
					? E('span', { 'class': 'ofi-address-list ofi-router-addresses' },
						routerAddresses.map(function(address) {
							return addressBadge(address, true);
						}))
					: '—'
			])
		]);
		dom.content(this.root, content);
		ui.tabs.initTabGroup(tabGroup.childNodes);
		if (this.pendingDetailFocus) {
			this.pendingDetailFocus = false;
			window.requestAnimationFrame(function() {
				var panel = self.root && self.root.querySelector('[data-ofi-panel="connections"]');
				if (!panel) return;
				panel.scrollIntoView({ block: 'start' });
				try {
					panel.focus({ preventScroll: true });
				} catch (e) {
					panel.focus();
				}
			});
		}
	},

	handleSaveApply: null,
	handleSave: null,
	handleReset: null
});
