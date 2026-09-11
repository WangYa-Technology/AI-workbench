ALTER TABLE system_setting_revisions
  ADD COLUMN site_configuration jsonb NOT NULL DEFAULT '{
    "siteName": "HCAI CHAT",
    "serverUrl": "http://127.0.0.1:8080",
    "siteIconUrl": "/brand/logo.png",
    "footerText": {
      "enUS": "Local Test product terms · Production legal acceptance pending",
      "zhCN": "本地测试产品条款 · 上线前仍需法律验收"
    },
    "policies": {
      "terms": { "enUS": "Account eligibility, acceptable transactions, platform responsibilities, and service limitations.", "zhCN": "账户资格、允许的交易、平台责任和服务限制。" },
      "privacy": { "enUS": "Data collection, use, export, deletion, retention, and Provider boundaries.", "zhCN": "数据收集、使用、导出、删除、保留和 Provider 边界。" },
      "cookies": { "enUS": "Essential session storage and future consent requirements for optional analytics.", "zhCN": "必要会话存储，以及未来可选分析功能的同意要求。" },
      "acceptable": { "enUS": "Safety, abuse prevention, prohibited content, and responsible AI use.", "zhCN": "安全、滥用防护、禁止内容和负责任的 AI 使用。" },
      "ai": { "enUS": "Model source, material inputs, limitations, and disclosure requirements for published work.", "zhCN": "模型来源、素材输入、限制和已发布作品的披露要求。" },
      "licensing": { "enUS": "Usage rights, attribution, derivatives, redistribution, and license version evidence.", "zhCN": "使用权、署名、衍生、再分发和授权版本证据。" },
      "refunds": { "enUS": "Local Test refund states and the external payment conditions required before production.", "zhCN": "本地测试退款状态，以及上线真实支付前必须满足的外部条件。" },
      "copyright": { "enUS": "Bounded complaint intake, stable target references, response records, and appeal pathways.", "zhCN": "有限信息投诉、稳定目标引用、响应记录和申诉路径。" }
    }
  }'::jsonb,
  ADD CONSTRAINT system_setting_site_configuration_object
    CHECK (jsonb_typeof(site_configuration) = 'object');
