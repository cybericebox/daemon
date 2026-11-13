-- The VPN and Internet gateways are devices of the task now (devices[].type vpn_gateway / internet_gateway). They are settings of
-- a lab, not a lab: a variant whose only devices are gateways has no infrastructure, the same as the Go rule
-- Topology.HasLab. Only a device object that is not a gateway counts; a device without a type is not a gateway.
CREATE OR REPLACE FUNCTION exercise_variants_have_infrastructure(p_variants jsonb)
    RETURNS boolean
    LANGUAGE sql
    IMMUTABLE
AS
$$
SELECT COALESCE(jsonb_path_exists(p_variants,
                                  '$[*].topology.devices[*] ? (@.type() == "object" && !exists(@.type ? (@ == "vpn_gateway" || @ == "internet_gateway")))'),
                false)
$$;

-- Whether the text of a task offers an address in the network of a VPN gateway: a gateway.subnet placeholder, or an ip placeholder
-- with ip_reference gateway, whose gateway is a vpn_gateway device of the variant that holds the task. The placeholder names
-- the gateway only; its kind is read from the devices of the variant (p_variants, the pinned version's variants; p_task_id, the
-- task whose snapshot p_snapshot is), so a placeholder of an Internet gateway is not VPN access.
CREATE FUNCTION task_offers_vpn_access(p_snapshot jsonb, p_variants jsonb, p_task_id uuid)
    RETURNS boolean
    LANGUAGE sql
    IMMUTABLE
AS
$$
SELECT COALESCE(EXISTS (SELECT 1
                        FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p_variants) = 'array' THEN p_variants ELSE '[]'::jsonb END) AS v(variant)
                                 CROSS JOIN jsonb_array_elements(CASE
                                                                     WHEN jsonb_typeof(p_snapshot -> 'placeholders') = 'array'
                                                                         THEN p_snapshot -> 'placeholders'
                                                                     ELSE '[]'::jsonb END) AS p(placeholder)
                        WHERE jsonb_path_exists(v.variant, '$.tasks[*] ? (@.id == $id)', jsonb_build_object('id', p_task_id::text))
                          AND (p.placeholder ->> 'kind' = 'gateway.subnet'
                            OR (p.placeholder ->> 'kind' = 'ip' AND p.placeholder ->> 'ip_reference' = 'gateway'))
                          AND jsonb_path_exists(v.variant, '$.topology.devices[*] ? (@.name == $gateway && @.type == "vpn_gateway")',
                                                jsonb_build_object('gateway', p.placeholder ->> 'gateway'))), false)
$$;
