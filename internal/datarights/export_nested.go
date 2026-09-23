package datarights

// Child queries repeat ownership predicates as well as the parent's identity.
// They retain the former field allowlists and deterministic per-parent ordering.
var exportNestedQueries = map[string][]exportChildQuery{
	"developerWebhookDeliveries": {{"attempts", `SELECT jsonb_build_object(
 'attemptNumber',a.attempt_number,'statusCode',a.status_code,'errorCode',a.error_code,
 'responseSha256',a.response_sha256,'durationMs',a.duration_ms,'attemptedAt',a.attempted_at)
 FROM developer_webhook_delivery_attempts a
 JOIN developer_webhook_deliveries d ON d.id=a.delivery_id
 JOIN developer_webhook_endpoints e ON e.id=d.endpoint_id
 WHERE e.owner_id=$1 AND d.id=$2 ORDER BY a.attempt_number`}},
	"identityEmailActions": {{"attempts", `SELECT jsonb_build_object(
 'attemptNumber',d.attempt_number,'adapter',d.adapter,'status',d.status,'errorCode',d.error_code,
 'receiptSha256',d.receipt_sha256,'attemptedAt',d.attempted_at)
 FROM identity_email_delivery_attempts d JOIN identity_email_actions a ON a.id=d.action_id
 WHERE a.user_id=$1 AND a.id=$2 ORDER BY d.attempt_number`}},
	"supportCases": {
		{"messages", `SELECT jsonb_build_object('id',m.id,'authorRole',m.author_role,'body',m.body,'createdAt',m.created_at)
 FROM support_messages m JOIN support_cases c ON c.id=m.case_id
 WHERE c.requester_id=$1 AND c.id=$2 ORDER BY m.created_at,m.id`},
		{"events", `SELECT jsonb_build_object('id',e.id,'kind',e.kind,'fromStatus',e.from_status,'toStatus',e.to_status,
 'reason',e.reason,'metadata',e.metadata,'createdAt',e.created_at)
 FROM support_events e JOIN support_cases c ON c.id=e.case_id
 WHERE c.requester_id=$1 AND c.id=$2 ORDER BY e.created_at,e.id`},
	},
	"refundReadReceipts": {{"observations", `SELECT jsonb_build_object('providerId',x->'providerId','providerPaymentId',x->'providerPaymentId',
 'amountCents',x->'amountCents','currency',x->'currency','status',x->'status','operationId',x->'operationId')
 FROM product_refund_read_receipts e JOIN product_refund_checks c ON c.id=e.check_id
 JOIN payment_intents p ON p.id=c.payment_id JOIN orders o ON o.id=p.order_id,
 LATERAL jsonb_array_elements(e.observations) WITH ORDINALITY AS v(x,n)
 WHERE p.purpose='product' AND p.payer_id=$1 AND o.buyer_id=$1 AND e.id=$2 ORDER BY n`}},
	"refundChecks": {{"readExecutions", `SELECT jsonb_build_object('id',r.id,'attemptNumber',r.attempt_number,
 'startedAt',r.started_at,'readDeadline',r.read_deadline,'recordedAt',r.recorded_at,'complete',r.complete,'errorCode',r.error_code,
 'recoveryReadId',v.recovery_execution_id,'recoveredAt',v.created_at)
 FROM product_refund_read_executions r JOIN product_refund_checks c ON c.id=r.check_id
 JOIN payment_intents p ON p.id=c.payment_id JOIN orders o ON o.id=p.order_id
 LEFT JOIN product_refund_read_recoveries v ON v.execution_id=r.id
 WHERE p.purpose='product' AND p.payer_id=$1 AND o.buyer_id=$1 AND c.id=$2 ORDER BY r.started_at,r.id`}, {"observations", `SELECT jsonb_build_object('providerId',x->'providerId','providerPaymentId',x->'providerPaymentId',
 'amountCents',x->'amountCents','currency',x->'currency','status',x->'status','operationId',x->'operationId')
 FROM product_refund_checks e JOIN payment_intents p ON p.id=e.payment_id JOIN orders o ON o.id=p.order_id,
 LATERAL jsonb_array_elements(e.observations) WITH ORDINALITY AS v(x,n)
 WHERE p.purpose='product' AND p.payer_id=$1 AND o.buyer_id=$1 AND e.id=$2 ORDER BY n`}},
}
