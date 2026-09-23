package datarights

import "github.com/hcai-chat/hcai-chat/internal/sellerfunds"

// Every projection is an allowlist. In particular, never export raw provider
// evidence, original request/return URLs, internal storage locations or staff
// recovery reasons. Adding a database column must not silently expand disclosure.
// Transaction evidence belongs to the buyer; sellers get a separate sales
// projection without the buyer's identity, payment identifiers or refund notes.
const buyerPaymentExportJoin = ` JOIN payment_intents p ON p.id=e.payment_id
 JOIN orders o ON o.id=p.order_id WHERE p.purpose='product' AND p.payer_id=$1 AND o.buyer_id=$1 `

var marketplaceExportQueries = []struct{ name, query string }{
	// Flat rows preserve the existing export cursor/size budgets; never aggregate
	// all accounts or histories into one unbounded JSON record.
	{"sellerFundsReconciliation", `SELECT jsonb_build_object('version',1,'asOf',transaction_timestamp(),
 'unresolvedRecords',(SELECT count(*) FROM seller_funds_unscoped WHERE seller_id=$1))`},
	{"sellerFundsAccounts", `SELECT jsonb_build_object('accountId',a.account_id,'provider',a.provider,
 'environment',CASE WHEN a.live_mode THEN 'live' ELSE 'test' END,'currency',a.currency,
 'pendingCents',a.pending_cents,'eligibleCreditCents',a.eligible_credit_cents,
 'bankDebitedCents',a.bank_debited_cents,'bankReturnedCents',a.bank_returned_cents,
 'availableCents',a.available_cents,'reservedCents',a.reserved_cents,
 'recoveryDueCents',a.recovery_due_cents,'withdrawableCents',a.withdrawable_cents)
 FROM (` + sellerfunds.AccountsQuery + `) a ORDER BY a.live_mode DESC,a.provider,a.account_id`},
	{"sellerFundsUnresolvedRecords", `SELECT jsonb_build_object('kind',kind,'id',id)
 FROM seller_funds_unscoped WHERE seller_id=$1 ORDER BY kind,id`},
	// Seller evidence is scoped independently of buyer transactions. Keep
	// internal review notes, provider identities, command keys and arbitrary
	// JSON evidence out of these projections, including historical records.
	{"sellerSettlements", `SELECT jsonb_build_object('id',s.id,'accountId',scope.account_id,'orderId',s.order_id,'provider',s.provider,
 'liveMode',s.live_mode,'grossAmountCents',s.gross_amount_cents,'feeBps',s.fee_bps,'feeCents',s.fee_cents,
 'netAmountCents',s.net_amount_cents,'currency',s.currency,'status',s.status,'availableAt',s.available_at,
 'recoveryAmountCents',s.recovery_amount_cents,'transferredAt',s.transferred_at,'createdAt',s.created_at,'updatedAt',s.updated_at)
 FROM product_settlements s JOIN seller_funds_settlement_scopes scope ON scope.settlement_id=s.id AND scope.seller_id=s.seller_id WHERE s.seller_id=$1 ORDER BY s.created_at,s.id`},
	{"sellerLedger", `SELECT jsonb_build_object('id',e.id,'accountId',scope.account_id,'entryType',e.entry_type,'amountCents',e.amount_cents,
 'currency',e.currency,'settlementId',e.settlement_id,'payoutRequestId',e.payout_request_id,
 'availableAt',e.available_at,'createdAt',e.created_at)
 FROM seller_ledger_entries e JOIN seller_funds_ledger_scopes scope ON scope.ledger_id=e.id AND scope.seller_id=e.seller_id WHERE e.seller_id=$1 ORDER BY e.created_at,e.id`},
	{"sellerRecoveryObligations", `SELECT jsonb_build_object('id',r.id,'accountId',scope.account_id,'settlementId',r.settlement_id,
 'amountCents',r.amount_cents,'remainingCents',r.remaining_cents,'currency',r.currency,'status',r.status,
 'createdAt',r.created_at,'updatedAt',r.updated_at)
 FROM seller_recovery_obligations r JOIN seller_funds_recovery_scopes scope ON scope.recovery_id=r.id AND scope.seller_id=r.seller_id WHERE r.seller_id=$1 ORDER BY r.created_at,r.id`},
	{"sellerPayoutRequests", `SELECT jsonb_build_object('id',r.id,'accountId',scope.account_id,'amountCents',r.amount_cents,'currency',r.currency,
 'status',r.status,'failureCode',r.failure_code,'createdAt',r.created_at,'updatedAt',r.updated_at)
 FROM seller_payout_requests r JOIN seller_funds_request_scopes scope ON scope.payout_request_id=r.id AND scope.seller_id=r.seller_id WHERE r.seller_id=$1 ORDER BY r.created_at,r.id`},
	{"sellerPayoutAllocations", `SELECT jsonb_build_object('payoutRequestId',a.payout_request_id,'settlementId',a.settlement_id,
 'amountCents',a.amount_cents,'createdAt',a.created_at,'releasedAt',a.released_at)
 FROM seller_payout_request_allocations a JOIN seller_payout_requests r ON r.id=a.payout_request_id
 JOIN product_settlements s ON s.id=a.settlement_id AND s.seller_id=r.seller_id
 WHERE r.seller_id=$1 ORDER BY a.created_at,a.payout_request_id,a.settlement_id`},
	{"sellerPayoutBankTargets", `SELECT jsonb_build_object('payoutRequestId',b.payout_request_id,'settlementId',b.settlement_id,
 'destinationId',b.destination_id,'bankDestinationId',b.bank_destination_id,'bankName',b.bank_name,'last4',b.last4,'amountCents',b.amount_cents,
 'currency',b.currency,'observedAt',b.observed_at,'createdAt',b.created_at)
 FROM seller_payout_bank_targets b JOIN seller_payout_requests r ON r.id=b.payout_request_id AND r.seller_id=b.seller_id
 WHERE r.seller_id=$1 ORDER BY b.created_at,b.payout_request_id`},
	{"sellerPayoutReviews", `SELECT jsonb_build_object('payoutRequestId',v.payout_request_id,'revision',v.revision,
 'decision',v.decision,'sellerMessage',v.seller_message,'createdAt',v.created_at)
 FROM seller_payout_reviews v JOIN seller_payout_requests r ON r.id=v.payout_request_id
 WHERE r.seller_id=$1 ORDER BY v.payout_request_id,v.revision`},
	{"sellerPayoutFundingAdmissions", `SELECT jsonb_build_object('id',a.id,'payoutRequestId',a.payout_request_id,
 'transferId',a.transfer_id,'reviewRevision',v.revision,'createdAt',a.created_at)
 FROM seller_payout_funding_admissions a JOIN seller_payout_requests r ON r.id=a.payout_request_id
 JOIN seller_payout_reviews v ON v.id=a.review_id AND v.payout_request_id=r.id
 WHERE r.seller_id=$1 ORDER BY a.created_at,a.id`},
	{"sellerBankPayoutCommands", `SELECT jsonb_build_object('id',c.id,'payoutRequestId',c.payout_request_id,
 'sourceTransferId',c.source_transfer_id,'reviewRevision',c.review_revision,'amountCents',c.amount_cents,
 'currency',c.currency,'bankDestinationId',c.bank_destination_id,'createdAt',c.created_at)
 FROM seller_bank_payout_commands c JOIN seller_payout_requests r ON r.id=c.payout_request_id AND r.seller_id=c.seller_id
 WHERE r.seller_id=$1 ORDER BY c.created_at,c.id`},
	{"sellerBankPayoutResumes", `SELECT jsonb_build_object('id',b.id,'commandId',b.command_id,'revision',b.revision,'createdAt',b.created_at)
 FROM seller_bank_payout_resumes b JOIN seller_bank_payout_commands c ON c.id=b.command_id
 JOIN seller_payout_requests r ON r.id=c.payout_request_id AND r.seller_id=c.seller_id
 WHERE r.seller_id=$1 ORDER BY b.created_at,b.id`},
	{"sellerBankPayoutResults", `SELECT jsonb_build_object('id',b.id,'commandId',b.command_id,'status',b.status,'createdAt',b.created_at)
 FROM seller_bank_payout_results b JOIN seller_bank_payout_commands c ON c.id=b.command_id
 JOIN seller_payout_requests r ON r.id=c.payout_request_id AND r.seller_id=c.seller_id
 WHERE r.seller_id=$1 ORDER BY b.created_at,b.id`},
	{"sellerBankPayoutReads", `SELECT jsonb_build_object('id',b.id,'commandId',b.command_id,'kind',b.kind,
 'outcome',b.outcome,'requiresReview',b.requires_review,'errorCode',b.error_code,'startedAt',b.started_at,'finishedAt',b.finished_at)
 FROM seller_bank_payout_reads b JOIN seller_bank_payout_commands c ON c.id=b.command_id
 JOIN seller_payout_requests r ON r.id=c.payout_request_id AND r.seller_id=c.seller_id
 WHERE r.seller_id=$1 ORDER BY b.started_at,b.id`},
	{"sellerSourceReversalCommands", `SELECT jsonb_build_object('id',c.id,'payoutRequestId',c.payout_request_id,
 'sourceTransferId',c.source_transfer_id,'settlementId',c.settlement_id,'amountCents',c.amount_cents,
 'currency',c.currency,'liveMode',c.live_mode,'createdAt',c.created_at)
 FROM seller_source_reversal_commands c JOIN seller_payout_requests r ON r.id=c.payout_request_id AND r.seller_id=c.seller_id
 WHERE r.seller_id=$1 ORDER BY c.created_at,c.id`},
	{"sellerSourceReversalReads", `SELECT jsonb_build_object('id',b.id,'commandId',b.command_id,'kind',b.kind,
 'outcome',b.outcome,'requiresReview',b.requires_review,'startedAt',b.started_at,'finishedAt',b.finished_at)
 FROM seller_source_reversal_reads b JOIN seller_source_reversal_commands c ON c.id=b.command_id
 JOIN seller_payout_requests r ON r.id=c.payout_request_id AND r.seller_id=c.seller_id
 WHERE r.seller_id=$1 ORDER BY b.started_at,b.id`},
	{"sellerSourceReversalResults", `SELECT jsonb_build_object('commandId',b.command_id,'readId',b.read_id,'createdAt',b.created_at)
 FROM seller_source_reversal_results b JOIN seller_source_reversal_commands c ON c.id=b.command_id
 JOIN seller_payout_requests r ON r.id=c.payout_request_id AND r.seller_id=c.seller_id
 WHERE r.seller_id=$1 ORDER BY b.created_at,b.command_id`},
	{"sellerSourceReversalClosures", `SELECT jsonb_build_object('id',b.id,'commandId',b.command_id,
 'payoutRequestId',b.payout_request_id,'readId',b.read_id,'settlementId',b.settlement_id,
 'resolution',b.resolution,'amountCents',b.amount_cents,'currency',b.currency,'createdAt',b.created_at)
 FROM seller_source_reversal_closures b JOIN seller_payout_requests r ON r.id=b.payout_request_id AND r.seller_id=b.seller_id
 WHERE r.seller_id=$1 ORDER BY b.created_at,b.id`},
	{"sellerPayoutEvents", `SELECT jsonb_build_object('id',e.id,'payoutRequestId',e.payout_request_id,
 'eventType',e.event_type,'fromStatus',e.from_status,'toStatus',e.to_status,'createdAt',e.created_at)
 FROM seller_payout_request_events e JOIN seller_payout_requests r ON r.id=e.payout_request_id
 WHERE r.seller_id=$1 ORDER BY e.created_at,e.id`},
	{"sellerPayoutSourceTransfers", `SELECT jsonb_build_object('id',t.id,'payoutRequestId',t.payout_request_id,
 'settlementId',t.settlement_id,'provider',t.provider,'liveMode',t.live_mode,'amountCents',t.amount_cents,
 'currency',t.currency,'status',t.status,'errorCode',t.error_code,'createdAt',t.created_at,'updatedAt',t.updated_at)
 FROM seller_payout_transfers t JOIN seller_payout_requests r ON r.id=t.payout_request_id
 JOIN product_settlements s ON s.id=t.settlement_id AND s.seller_id=r.seller_id
 WHERE r.seller_id=$1 ORDER BY t.created_at,t.id`},
	{"rejectedProviderEvents", `SELECT jsonb_build_object('id',q.id,'paymentId',p.id,'provider',q.provider,
 'providerEventId',q.provider_event_id,'eventType',q.event->>'EventType','liveMode',q.live_mode,
 'state',q.state,'rejectionCode',q.rejection_code,'lastErrorCode',q.last_error_code,
 'admittedEventId',q.admitted_event_id,'version',q.version,'receivedAt',q.received_at,'checkedAt',q.checked_at)
 FROM product_webhook_quarantines q LEFT JOIN payment_provider_events e ON e.id=q.admitted_event_id
 JOIN payment_intents p ON p.id=COALESCE(q.candidate_payment_id,e.payment_id)
 JOIN orders o ON o.id=p.order_id
 WHERE p.purpose='product' AND p.payer_id=$1 AND o.buyer_id=$1 ORDER BY q.received_at,q.id`},
	{"rejectedProviderEventChecks", `SELECT jsonb_build_object('id',c.id,'quarantineId',q.id,'paymentId',p.id,
 'expectedVersion',c.expected_version,'outcome',c.outcome,'createdAt',c.created_at)
 FROM product_webhook_quarantine_checks c JOIN product_webhook_quarantines q ON q.id=c.quarantine_id
 LEFT JOIN payment_provider_events e ON e.id=q.admitted_event_id
 JOIN payment_intents p ON p.id=COALESCE(q.candidate_payment_id,e.payment_id)
 JOIN orders o ON o.id=p.order_id
 WHERE p.purpose='product' AND p.payer_id=$1 AND o.buyer_id=$1 ORDER BY c.created_at,c.id`},
	{"deliveryRepairs", `SELECT jsonb_build_object('orderId',r.order_id,'revision',r.revision,'state',r.state,'createdAt',r.created_at,'readyAt',r.ready_at,'removedAt',r.removed_at)
 FROM product_delivery_repairs r JOIN orders o ON o.id=r.order_id WHERE o.buyer_id=$1 ORDER BY r.order_id,r.revision`},
	{"products", `SELECT jsonb_build_object('id',id,'assetId',asset_id,'title',title,'description',description,
  'productType',product_type,'priceCents',price_cents,'currency',currency,'licenseCode',license_code,
  'status',status,'aiDisclosure',ai_disclosure,'includedFiles',included_files,'compatibility',compatibility,
  'previewAssetId',preview_asset_id,'createdAt',created_at,'updatedAt',updated_at,
  'files',COALESCE((SELECT jsonb_agg(jsonb_build_object('assetId',f.asset_id,'name',f.file_name) ORDER BY f.position)
    FROM product_listing_files f WHERE f.product_id=p.id),'[]'::jsonb))
 FROM products p WHERE seller_id=$1 ORDER BY created_at,id`},
	{"listingHistory", `SELECT jsonb_build_object('productId',product_id,'action',action,'createdAt',created_at,
      'version',snapshot->'version','contentVersion',snapshot->'contentVersion','status',snapshot->'status',
      'reviewStatus',snapshot->'reviewStatus','reviewReason',snapshot->'reviewReason',
      'title',snapshot->'title','description',snapshot->'description','productType',snapshot->'productType',
      'category',snapshot->'category','assetId',snapshot->'assetId','previewAssetId',snapshot->'previewAssetId',
      'priceCents',snapshot->'priceCents','currency',snapshot->'currency','licenseCode',snapshot->'licenseCode',
      'aiDisclosure',snapshot->'aiDisclosure','includedFiles',snapshot->'includedFiles','compatibility',snapshot->'compatibility',
      'files',COALESCE((SELECT jsonb_agg(jsonb_build_object('assetId',f.value->'assetId','name',f.value->'name') ORDER BY f.ordinality)
        FROM jsonb_array_elements(COALESCE(snapshot->'files','[]'::jsonb)) WITH ORDINALITY f),'[]'::jsonb))
      FROM product_listing_commands WHERE snapshot->>'sellerId'=$1::uuid::text ORDER BY created_at,key_sha256`},
	{"sales", `SELECT jsonb_build_object('orderId',o.id,'productId',o.product_id,'amountCents',o.amount_cents,
  'currency',o.currency,'status',o.status,'licenseAcceptedAt',o.license_accepted_at,
  'createdAt',o.created_at,'updatedAt',o.updated_at)
 FROM orders o JOIN product_sale_owners own ON own.order_id=o.id
 WHERE own.seller_id=$1::uuid::text
 ORDER BY o.created_at,o.id`},
	{"entitlements", `SELECT jsonb_build_object('id',e.id,'productId',e.product_id,'orderId',e.order_id,
  'assetId',e.asset_id,'licenseCode',e.license_code,'status',e.status,'grantedAt',e.granted_at,'revokedAt',e.revoked_at)
 FROM entitlements e JOIN orders o ON o.id=e.order_id WHERE e.user_id=$1 AND o.buyer_id=$1 ORDER BY e.granted_at,e.id`},
	{"orderEvents", `SELECT jsonb_build_object('id',e.id,'orderId',e.order_id,'sequence',e.sequence,
  'fromStatus',e.from_status,'toStatus',e.to_status,'reason',e.reason,'createdAt',e.created_at)
 FROM order_events e JOIN orders o ON o.id=e.order_id WHERE o.buyer_id=$1 ORDER BY e.order_id,e.sequence,e.id`},
	{"contracts", `SELECT jsonb_build_object('orderId',c.order_id,'offerVersion',c.offer_version,'acceptedAt',c.accepted_at,
  'product',jsonb_build_object('id',c.contract->'product'->'id','sellerId',c.contract->'product'->'sellerId',
   'title',c.contract->'product'->'title','description',c.contract->'product'->'description',
   'productType',c.contract->'product'->'productType','priceCents',c.contract->'product'->'priceCents',
   'currency',c.contract->'product'->'currency','aiDisclosure',c.contract->'product'->'aiDisclosure',
   'includedFiles',c.contract->'product'->'includedFiles','compatibility',c.contract->'product'->'compatibility'),
  'license',jsonb_build_object('code',c.contract->'license'->'code','name',c.contract->'license'->'name',
   'summary',c.contract->'license'->'summary','version',c.contract->'license'->'version','terms',c.contract->'license'->'terms',
   'allowsCommercial',c.contract->'license'->'allowsCommercial','allowsDerivatives',c.contract->'license'->'allowsDerivatives',
   'allowsRedistribution',c.contract->'license'->'allowsRedistribution','attributionRequired',c.contract->'license'->'attributionRequired',
   'refundWindowDays',c.contract->'license'->'refundWindowDays'),
  'asset',jsonb_build_object('id',c.source_asset_id,'rootId',c.root_asset_id,'familyId',c.contract->'asset'->'familyId',
   'version',c.contract->'asset'->'version','title',c.contract->'asset'->'title','kind',c.contract->'asset'->'kind',
   'mimeType',c.contract->'asset'->'mimeType','width',c.contract->'asset'->'width','height',c.contract->'asset'->'height'))
 || CASE WHEN c.contract->'delivery'->>'format'='zip-v1' THEN jsonb_build_object('delivery',jsonb_build_object('format','zip-v1','files',
   (SELECT jsonb_agg(jsonb_build_object('position',f.value->'position','assetId',f.value->'assetId',
     'name',f.value->'name','mimeType',f.value->'source'->'mimeType') ORDER BY f.ordinality)
    FROM jsonb_array_elements(c.contract->'delivery'->'files') WITH ORDINALITY f))) ELSE '{}'::jsonb END
 FROM product_order_contracts c JOIN orders o ON o.id=c.order_id WHERE o.buyer_id=$1 ORDER BY c.accepted_at,c.order_id`},
	{"deliverySnapshots", `SELECT jsonb_build_object('orderId',d.order_id,'sha256',d.sha256,'sizeBytes',d.size_bytes,
  'mimeType',d.mime_type,'state',d.state,'createdAt',d.created_at,'readyAt',d.ready_at,'removedAt',d.removed_at)
 || CASE WHEN d.format='zip-v1' THEN jsonb_build_object('format',d.format,'fileManifest',jsonb_build_object('version',1,'files',
   (SELECT jsonb_agg(jsonb_build_object('name',f.value->'name','mimeType',f.value->'mimeType',
      'sizeBytes',f.value->'sizeBytes','sha256',f.value->'sha256') ORDER BY f.ordinality)
    FROM jsonb_array_elements(d.file_manifest->'files') WITH ORDINALITY f))) ELSE '{}'::jsonb END
 FROM product_delivery_snapshots d JOIN orders o ON o.id=d.order_id WHERE o.buyer_id=$1 ORDER BY d.created_at,d.order_id`},
	{"payments", `SELECT jsonb_build_object('id',p.id,'orderId',p.order_id,'productId',p.resource_id,'provider',p.provider,
  'amountCents',p.amount_cents,'currency',p.currency,'status',p.status,'liveMode',p.live_mode,'version',p.version,
  'providerCheckoutId',p.provider_checkout_id,'checkoutExpiresAt',p.checkout_expires_at,'providerPaymentId',p.provider_payment_id,
  'providerChargeId',p.provider_charge_id,'providerRefundId',p.provider_refund_id,'compensationReason',p.compensation_reason,
  'paidAt',p.paid_at,'refundedAt',p.refunded_at,'createdAt',p.created_at,'updatedAt',p.updated_at)
 FROM payment_intents p JOIN orders o ON o.id=p.order_id WHERE p.purpose='product' AND p.payer_id=$1 AND o.buyer_id=$1 ORDER BY p.created_at,p.id`},
	{"paymentEvents", `SELECT jsonb_build_object('id',e.id,'paymentId',e.payment_id,'providerEventId',e.provider_event_id,
  'eventType',e.event_type,'fromStatus',e.from_status,'toStatus',e.to_status,'createdAt',e.created_at)
 || COALESCE((SELECT jsonb_build_object('checkoutJobId',j.id,'lookupJobId',l.job_id)
   FROM jobs j JOIN product_checkout_lookups l ON l.job_id::text=e.evidence->>'lookupJobId' AND l.payment_id=e.payment_id
   WHERE e.event_type='checkout.queried' AND j.id::text=e.evidence->>'jobId'
   AND j.kind='payment.check_product_checkout' AND j.payload->>'paymentId'=e.payment_id::text),'{}'::jsonb)
 || COALESCE((SELECT jsonb_build_object('checkoutJobId',j.id,'identityRecoveryJobId',r.job_id)
   FROM jobs j JOIN product_payment_identity_recoveries r ON r.job_id::text=e.evidence->>'identityRecoveryJobId' AND r.payment_id=e.payment_id
   WHERE e.event_type='checkout.queried' AND j.id::text=e.evidence->>'jobId'
   AND j.kind='payment.check_product_checkout' AND j.payload->>'paymentId'=e.payment_id::text),'{}'::jsonb)
 FROM payment_intent_events e ` + buyerPaymentExportJoin + ` ORDER BY e.created_at,e.id`},
	{"providerEvents", `SELECT jsonb_build_object('id',e.id,'paymentId',e.payment_id,'provider',e.provider,
  'providerEventId',e.provider_event_id,'eventType',e.event_type,'evidenceSource',e.evidence_source,
  'payloadSha256',e.payload_sha256,'liveMode',e.live_mode,'amountCents',e.amount_cents,'currency',e.currency,
  'paymentStatus',e.payment_status,'refundOperationId',e.refund_operation_id,'refundCheckId',e.refund_check_id,
  'checkoutJobId',e.checkout_job_id,'occurredAt',e.occurred_at,'receivedAt',e.received_at,
  'processingStatus',s.status,'errorCode',s.error_code,'processedAt',s.processed_at,
  'waffoBinding',(SELECT jsonb_build_object('contractVersion',b.contract_version,'storeId',b.store_id,
    'liveMode',b.live_mode,'orderId',b.order_id,'verifiedAt',b.created_at)
    FROM product_waffo_webhook_bindings b WHERE b.event_id=e.id AND b.payment_id=e.payment_id))
 FROM payment_provider_events e LEFT JOIN payment_provider_event_processing s ON s.event_id=e.id ` + buyerPaymentExportJoin + ` ORDER BY e.occurred_at,e.id`},
	{"refundAttempts", `SELECT jsonb_build_object('operationId',e.operation_id,'paymentId',e.payment_id,
  'provider',e.provider,'providerPaymentId',e.provider_payment_id,'providerRefundId',e.provider_refund_id,
  'amountCents',e.amount_cents,'currency',e.currency,'status',e.status,'reconciliationRequired',
    (e.reconciliation_required OR EXISTS(SELECT 1 FROM product_refund_dispatch_review d WHERE d.operation_id=e.operation_id)),
  'requestedAt',e.requested_at,'updatedAt',e.updated_at,
  'dispatch',(SELECT jsonb_build_object('contractVersion',d.contract_version,'createdAt',d.created_at,
    'reservedAt',d.reserved_at,'respondedAt',d.responded_at,'providerRefundId',d.provider_refund_id)
    FROM product_refund_dispatches d WHERE d.operation_id=e.operation_id))
 FROM product_refund_attempts e ` + buyerPaymentExportJoin + ` ORDER BY e.requested_at,e.operation_id`},
	{"refundReadReceipts", `SELECT jsonb_build_object('id',e.id,'checkId',e.check_id,'paymentId',c.payment_id,
 'attemptNumber',e.attempt_number,'complete',e.complete,'errorCode',e.error_code,'createdAt',e.created_at)
 FROM product_refund_read_receipts e JOIN product_refund_checks c ON c.id=e.check_id
 JOIN payment_intents p ON p.id=c.payment_id JOIN orders o ON o.id=p.order_id
 WHERE p.purpose='product' AND p.payer_id=$1 AND o.buyer_id=$1 ORDER BY e.created_at,e.id`},
	{"refundChecks", `SELECT jsonb_build_object('id',e.id,'paymentId',e.payment_id,'jobId',e.job_id,'status',e.status,'origin',e.origin,
  'unresolvedCount',e.unresolved_count,'errorCode',e.error_code,'createdAt',e.created_at,'observedAt',e.observed_at,'completedAt',e.completed_at)
 FROM product_refund_checks e ` + buyerPaymentExportJoin + ` ORDER BY e.created_at,e.id`},
	{"checkoutCommands", `SELECT jsonb_build_object('paymentId',e.payment_id,
  'commandSha256',encode(public.digest(e.idempotency_key,'sha256'),'hex'),'createdAt',e.created_at)
 FROM product_checkout_commands e ` + buyerPaymentExportJoin + ` AND e.buyer_id=$1 ORDER BY e.created_at,e.idempotency_key`},
	{"checkoutRequests", `SELECT jsonb_build_object('paymentId',e.payment_id,'dispatchProtocol',e.dispatch_protocol,'createdAt',e.created_at,
  'provider',e.identity->'provider','merchantId',e.identity->'merchantId','storeId',e.identity->'storeId',
  'liveMode',e.identity->'liveMode','apiVersion',e.identity->'apiVersion','requestVersion',e.identity->'requestVersion',
  'name',e.request->'Name','amountCents',e.request->'AmountCents','currency',e.request->'Currency',
  'buyerIdentity',e.request->'BuyerIdentity','buyerEmail',e.request->'BuyerEmail','productId',e.request->'ProductID',
  'productType',e.request->'ProductType','orderExternalId',e.request->'OrderExternalID')
 FROM product_checkout_requests e ` + buyerPaymentExportJoin + ` ORDER BY e.created_at,e.payment_id`},
	{"checkoutDispatches", `SELECT jsonb_build_object('paymentId',e.payment_id,'requestSha256',e.request_sha256,'reservedAt',e.reserved_at)
 FROM product_checkout_dispatches e ` + buyerPaymentExportJoin + ` ORDER BY e.reserved_at,e.payment_id`},
	{"checkoutClosures", `SELECT jsonb_build_object('paymentId',e.payment_id,'orderId',e.order_id,
  'commandSha256',encode(public.digest(e.idempotency_key,'sha256'),'hex'),'observedVersion',e.observed_version,'createdAt',e.created_at)
 FROM product_checkout_closures e ` + buyerPaymentExportJoin + ` AND e.buyer_id=$1 ORDER BY e.created_at,e.payment_id`},
	{"identityRecoveries", `SELECT jsonb_build_object('paymentId',e.payment_id,'jobId',e.job_id,'createdAt',e.created_at,
  'provider',e.identity->'provider','merchantId',e.identity->'merchantId','liveMode',e.identity->'liveMode',
  'apiVersion',e.identity->'apiVersion','requestVersion',e.identity->'requestVersion',
  'providerCheckoutId',e.binding->'providerCheckoutId','providerPaymentId',e.binding->'providerPaymentId')
 FROM product_payment_identity_recoveries e ` + buyerPaymentExportJoin + ` ORDER BY e.created_at,e.payment_id`},
	{"checkoutLookups", `SELECT jsonb_build_object('paymentId',e.payment_id,'jobId',e.job_id,'outcome',e.outcome,
  'searchedAfter',e.searched_after,'searchedBefore',e.searched_before,'checkJobId',e.check_job_id,'createdAt',e.created_at)
 FROM product_checkout_lookups e ` + buyerPaymentExportJoin + ` ORDER BY e.created_at,e.job_id`},
	{"closedCheckoutRecoveries", `SELECT jsonb_build_object('paymentId',e.payment_id,'providerEventId',e.event_id,
  'checkoutJobId',e.checkout_job_id,'fromPaymentStatus',e.from_payment_status,'fromOrderStatus',e.from_order_status,'createdAt',e.created_at)
 FROM product_closed_checkout_recoveries e ` + buyerPaymentExportJoin + ` ORDER BY e.created_at,e.payment_id`},
	{"closedCheckoutRefundConfirmations", `SELECT jsonb_build_object('paymentId',e.payment_id,'checkId',e.check_id,
 'providerEventId',e.event_id,'operationId',e.operation_id,'createdAt',e.created_at)
 FROM product_closed_checkout_refund_confirmations e ` + buyerPaymentExportJoin + ` ORDER BY e.created_at,e.payment_id`},
	{"checkoutCheckDispatches", `SELECT jsonb_build_object('paymentId',e.payment_id,'jobId',e.job_id,
  'paymentVersion',e.payment_version,'dueAt',e.due_at,'createdAt',e.created_at)
 FROM product_checkout_check_dispatches e ` + buyerPaymentExportJoin + ` ORDER BY e.created_at,e.payment_id`},
	{"cleanupJobs", `SELECT jsonb_build_object('id',j.id,'orderId',o.id,'status',j.status,'attempts',j.attempts,
  'maxAttempts',j.max_attempts,'errorCode',j.last_error_code,'createdAt',j.created_at,'updatedAt',j.updated_at,
  'replacementJobId',r.retry_job_id,'retryOf',parent.original_job_id,'recoveryRequestedAt',r.created_at)
 FROM jobs j JOIN orders o ON o.id::text=j.payload->>'orderId'
 LEFT JOIN media_cleanup_recoveries r ON r.original_job_id=j.id
 LEFT JOIN media_cleanup_recoveries parent ON parent.retry_job_id=j.id
 WHERE j.kind='product.delivery_cleanup' AND o.buyer_id=$1 ORDER BY j.created_at,j.id`},
}
