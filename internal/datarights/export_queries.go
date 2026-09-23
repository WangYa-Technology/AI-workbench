package datarights

// Row projections are streamed in one repeatable-read snapshot. Nested per-record
// evidence is streamed separately in exportNestedQueries, preserving the schema.
var accountExportQueries = map[string]string{
	"billingWebhookBindings": `SELECT jsonb_build_object('eventId',b.event_id,'paymentId',b.payment_id,
 'contractVersion',b.contract_version,'storeId',b.store_id,'liveMode',b.live_mode,'orderExternalId',b.order_external_id,
 'eventType',e.event_type,'purpose',e.purpose,'amountCents',e.amount_cents,'currency',e.currency,'createdAt',b.created_at)
 FROM billing_waffo_webhook_bindings b JOIN payment_intents p ON p.id=b.payment_id
 JOIN payment_provider_events e ON e.id=b.event_id AND e.payment_id=p.id
 WHERE p.payer_id=$1 AND b.buyer_id=$1 AND p.purpose IN ('wallet_topup','subscription') ORDER BY b.created_at,b.event_id`,
	"walletAdjustments": `SELECT jsonb_build_object('operationId',operation_id,'deltaCents',delta_cents,'currency',currency,'createdAt',created_at)
 FROM admin_finance_adjustments WHERE user_id=$1 ORDER BY created_at,operation_id`,
	"uploadCommands": `SELECT jsonb_build_object('id',id,'resultAssetId',result_asset_id,'createdAt',created_at,'completedAt',completed_at)
 FROM asset_upload_commands WHERE owner_id=$1 ORDER BY created_at,id`,
	"uploadAttempts": `SELECT jsonb_build_object('commandId',a.command_id,'writeId',a.write_id,'createdAt',a.created_at)
 FROM asset_upload_attempts a JOIN asset_upload_commands c ON c.id=a.command_id WHERE c.owner_id=$1 ORDER BY a.created_at,a.write_id`,
	"uploadWrites": `SELECT jsonb_build_object('id',id,'assetId',asset_id,'status',status,'sizeBytes',size_bytes,'createdAt',created_at,'attachedAt',attached_at,'verifiedAbsentAt',verified_absent_at,'cleanupChecks',cleanup_checks,'lastErrorCode',last_error_code)
 FROM upload_writes WHERE owner_id=$1 ORDER BY created_at,id`,
	"generationOutputWrites": `SELECT jsonb_build_object('id',id,'generationId',generation_id,'assetId',asset_id,'status',status,'sizeBytes',size_bytes,'createdAt',created_at,'attachedAt',attached_at,'verifiedAbsentAt',verified_absent_at,'cleanupChecks',cleanup_checks,'lastErrorCode',last_error_code)
 FROM generation_output_writes WHERE owner_id=$1 ORDER BY created_at,id`,
	"originalMediaCleanupReceipts": `SELECT jsonb_build_object('id',id,'outcome',outcome,'sizeBytes',size_bytes,'verifiedAt',verified_at)
 FROM original_media_cleanup_receipts WHERE owner_id=$1 ORDER BY verified_at,id`,
	"originalMediaCleanupReconciliations": `SELECT jsonb_build_object('requestId',r.request_id,'jobId',r.job_id,'previousJobId',r.previous_job_id,'status',j.status,'createdAt',r.created_at)
 FROM original_media_cleanup_reconciliations r JOIN jobs j ON j.id=r.job_id WHERE r.user_id=$1 ORDER BY r.created_at,r.job_id`,
	"accountDeletionReconciliations": `SELECT jsonb_build_object('requestId',r.request_id,'jobId',r.job_id,'previousJobId',r.previous_job_id,'stage',r.stage,'status',j.status,'createdAt',r.created_at)
 FROM account_deletion_reconciliations r JOIN jobs j ON j.id=r.job_id JOIN data_rights_requests request ON request.id=r.request_id
 WHERE request.user_id=$1 ORDER BY r.created_at,r.job_id`,
	"productCleanupReconciliations": `SELECT jsonb_build_object('orderId',r.order_id,'jobId',r.job_id,'previousJobId',r.previous_job_id,'status',j.status,'createdAt',r.created_at)
 FROM product_cleanup_reconciliations r JOIN jobs j ON j.id=r.job_id JOIN orders o ON o.id=r.order_id
 WHERE o.buyer_id=$1 ORDER BY r.created_at,r.job_id`,
	"holdCleanupChecks": `SELECT jsonb_build_object('holdId',hold_id,'createdAt',created_at,'completedAt',completed_at)
 FROM legal_hold_cleanup_checks WHERE user_id=$1 ORDER BY created_at,hold_id`,
	"retentionCleanupJobs": `SELECT jsonb_build_object('id',d.id,'kind',d.kind,'orderId',d.order_id,'jobId',d.job_id,
 'status',j.status,'createdAt',d.created_at)
 FROM legal_hold_cleanup_dispatches d JOIN jobs j ON j.id=d.job_id LEFT JOIN orders o ON o.id=d.order_id
 WHERE (d.kind='account' AND d.user_id=$1) OR (d.kind='product' AND o.buyer_id=$1)
 ORDER BY d.created_at,d.id`,
	"account":              `SELECT jsonb_build_object('id',id,'email',email,'handle',handle,'displayName',display_name,'role',role,'status',status,'locale',locale,'timezone',timezone,'createdAt',created_at,'updatedAt',updated_at) FROM users WHERE id=$1`,
	"sessions":             `SELECT jsonb_build_object('id',id,'clientLabel',client_label,'createdAt',created_at,'lastSeenAt',last_seen_at,'expiresAt',expires_at,'revokedAt',revoked_at) FROM sessions WHERE user_id=$1 ORDER BY created_at`,
	"task_delivery_grants": `SELECT to_jsonb(g) FROM task_delivery_grants g WHERE g.client_id=$1 OR g.creator_id=$1`,
	"assets":               `SELECT to_jsonb(a)-'media_url'-'storage_backend'-'storage_key' FROM assets a WHERE owner_id=$1 ORDER BY created_at,id`,
	"assetVersionEvents": `SELECT jsonb_build_object('id',e.id,'familyId',e.family_id,'assetId',e.asset_id,'previousAssetId',e.previous_asset_id,'eventType',e.event_type,'reason',e.reason,'createdAt',e.created_at)
			FROM asset_version_events e WHERE e.actor_id=$1 ORDER BY e.created_at,e.id`,
	"works":             `SELECT to_jsonb(w) FROM works w WHERE author_id=$1 ORDER BY created_at`,
	"generations":       `SELECT to_jsonb(g) FROM generations g WHERE owner_id=$1 ORDER BY created_at`,
	"communityPosts":    `SELECT to_jsonb(p) FROM posts p WHERE author_id=$1 ORDER BY created_at`,
	"communityComments": `SELECT to_jsonb(c) FROM comments c WHERE author_id=$1 ORDER BY created_at`,
	"savedWorks": `SELECT jsonb_build_object(
			'postId',pr.post_id,'workId',p.work_id,'savedAt',pr.created_at
		)
			FROM post_reactions pr JOIN posts p ON p.id=pr.post_id
			WHERE pr.user_id=$1 AND pr.kind='bookmark' ORDER BY pr.created_at,pr.post_id`,
	"communityFollows": `SELECT jsonb_build_object(
			'followingId',f.following_id,'createdAt',f.created_at
		)
			FROM user_follows f WHERE f.follower_id=$1 ORDER BY f.created_at,f.following_id`,
	"orders":     `SELECT to_jsonb(o)-'idempotency_key'-'refund_idempotency_key' FROM orders o WHERE buyer_id=$1 ORDER BY created_at,id`,
	"tasks":      `SELECT to_jsonb(d) FROM demands d WHERE client_id=$1 OR assignee_id=$1 ORDER BY created_at`,
	"proposals":  `SELECT to_jsonb(p) FROM proposals p WHERE creator_id=$1 ORDER BY created_at`,
	"deliveries": `SELECT to_jsonb(d) FROM deliveries d WHERE creator_id=$1 ORDER BY created_at`,
	"billing":    `SELECT to_jsonb(l) FROM ledger_entries l WHERE account_id=$1 ORDER BY created_at`,
	"notifications": `SELECT jsonb_build_object(
			'id',n.id,'kind',n.kind,'title',n.title,'body',n.body,'targetPath',n.target_path,
			'resourceType',n.resource_type,'resourceId',n.resource_id,'readAt',n.read_at,
			'deliveryStatus',n.delivery_status,'deliveryErrorCode',n.delivery_error_code,
			'deliveredAt',n.delivered_at,'suppressedAt',n.suppressed_at,'createdAt',n.created_at
		) FROM notifications n WHERE n.user_id=$1 ORDER BY n.created_at,n.id`,
	"developerWebhookEndpoints": `SELECT jsonb_build_object(
			'id',e.id,'name',e.name,'url',e.url,'eventTypes',e.event_types,'status',e.status,
			'currentSecretVersion',e.current_secret_version,'version',e.version,
			'createdAt',e.created_at,'updatedAt',e.updated_at,'revokedAt',e.revoked_at
		) FROM developer_webhook_endpoints e WHERE e.owner_id=$1 ORDER BY e.created_at,e.id`,
	"developerWebhookEvents": `SELECT jsonb_build_object(
			'id',e.id,'eventType',e.event_type,'resourceType',e.resource_type,'resourceId',e.resource_id,
			'payload',e.payload,'createdAt',e.created_at
		) FROM developer_webhook_events e WHERE e.owner_id=$1 ORDER BY e.created_at,e.id`,
	"developerWebhookDeliveries": `SELECT jsonb_build_object(
			'id',d.id,'endpointId',d.endpoint_id,'eventId',d.event_id,'status',d.status,'version',d.version,
			'attemptCount',d.attempt_count,'nextAttemptAt',d.next_attempt_at,'lastStatusCode',d.last_status_code,
			'lastErrorCode',d.last_error_code,'originalDeliveryId',d.original_delivery_id,
			'createdAt',d.created_at,'updatedAt',d.updated_at,'succeededAt',d.succeeded_at,'deadLetteredAt',d.dead_lettered_at
		)
		FROM developer_webhook_deliveries d
		JOIN developer_webhook_endpoints e ON e.id=d.endpoint_id WHERE e.owner_id=$1 ORDER BY d.created_at,d.id`,
	"identityEmailActions": `SELECT jsonb_build_object(
			'id',a.id,'kind',a.kind,'status',a.status,'locale',a.locale,'version',a.version,
			'attemptCount',a.attempt_count,'originalActionId',a.original_action_id,'expiresAt',a.expires_at,
			'createdAt',a.created_at,'updatedAt',a.updated_at,'deliveredAt',a.delivered_at,
			'consumedAt',a.consumed_at,'cancelledAt',a.cancelled_at,'deadLetteredAt',a.dead_lettered_at
		) FROM identity_email_actions a WHERE a.user_id=$1 ORDER BY a.created_at,a.id`,
	"supportCases": `SELECT jsonb_build_object(
			'id',c.id,'category',c.category,'subject',c.subject,'details',c.details,
			'relatedResourceType',c.related_resource_type,'relatedResourceId',c.related_resource_id,
			'locale',c.locale,'claimantRelationship',c.claimant_relationship,'rightsStatement',c.rights_statement,
			'status',c.status,'version',c.version,'resolutionCode',c.resolution_code,'resolutionReason',c.resolution_reason,
			'createdAt',c.created_at,'updatedAt',c.updated_at,'resolvedAt',c.resolved_at
		) FROM support_cases c WHERE c.requester_id=$1 ORDER BY c.created_at,c.id`,
	"riskSignals": `SELECT jsonb_build_object(
			'id',s.id,'resourceType',s.resource_type,'resourceId',s.resource_id,'signalType',s.signal_type,
			'severity',s.severity,'score',s.score,'status',s.status,'summary',s.summary,'evidence',s.evidence,
			'detectedAt',s.detected_at,'updatedAt',s.updated_at,'resolvedAt',s.resolved_at
		) FROM risk_signals s WHERE s.subject_user_id=$1 ORDER BY s.detected_at,s.id`,
	"audit": `SELECT jsonb_build_object('id',id,'action',action,'resourceType',resource_type,'resourceId',resource_id,'reason',reason,'requestId',request_id,'createdAt',created_at) FROM audit_events WHERE actor_id=$1 ORDER BY created_at`,
}
