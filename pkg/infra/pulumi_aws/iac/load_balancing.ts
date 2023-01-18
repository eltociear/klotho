import * as aws from '@pulumi/aws'
import * as validators from './sanitization/aws/elb'
import {
    ListenerArgs,
    LoadBalancerArgs,
    TargetGroupArgs,
    TargetGroupAttachmentArgs,
} from '@pulumi/aws/lb'
import { ListenerRuleArgs } from '@pulumi/aws/alb'
import { h, sanitized } from './sanitization/sanitizer'

export class LoadBalancerPlugin {
    // A map of all resources which are going to be fronted by a load balancer
    private resourceIdToLB = new Map<string, aws.lb.LoadBalancer>()

    public getExecUnitLoadBalancer = (execUnit: string): aws.lb.LoadBalancer | undefined => {
        return this.resourceIdToLB.get(execUnit)
    }

    public createLoadBalancer = (
        appName: string,
        resourceId: string,
        params: LoadBalancerArgs
    ): aws.lb.LoadBalancer => {
        let lb: aws.lb.LoadBalancer
        let lbName = sanitized(validators.loadBalancer.nameValidation())`${h(appName)}-${h(
            resourceId
        )}`
        switch (params.loadBalancerType) {
            case 'application':
                lb = new aws.lb.LoadBalancer(`${appName}-${resourceId}-alb`, {
                    name: lbName,
                    internal: params.internal || false,
                    loadBalancerType: 'application',
                    securityGroups: params.securityGroups,
                    subnets: params.subnets,
                    enableDeletionProtection: params.enableDeletionProtection || false,
                    tags: params.tags,
                })
                break
            case 'network':
                lb = new aws.lb.LoadBalancer(`${appName}-${resourceId}-nlb`, {
                    name: lbName,
                    internal: params.internal || true,
                    loadBalancerType: 'network',
                    subnets: params.subnets,
                    enableDeletionProtection: params.enableDeletionProtection || false,
                    tags: params.tags,
                })
                break
            default:
                throw new Error('Unsupported load balancer type')
        }
        this.resourceIdToLB.set(resourceId, lb)
        return lb
    }

    public createListener = (
        appName: string,
        resourceId: string,
        params: ListenerArgs
    ): aws.lb.Listener => {
        return new aws.lb.Listener(`${appName}-${resourceId}-listener`, {
            loadBalancerArn: params.loadBalancerArn,
            defaultActions: params.defaultActions,
            port: params.port,
            protocol: params.protocol,
        })
    }

    public createListenerRule = (
        appName: string,
        resourceId: string,
        params: ListenerRuleArgs
    ): aws.lb.ListenerRule => {
        return new aws.lb.ListenerRule(`${appName}-${resourceId}-listenerRule`, {
            listenerArn: params.listenerArn,
            actions: params.actions,
            conditions: params.conditions,
            priority: params.priority,
        })
    }

    public createTargetGroup = (
        appName: string,
        resourceId,
        params: TargetGroupArgs
    ): aws.lb.TargetGroup => {
        let targetGroup: aws.lb.TargetGroup
        let tgName = sanitized(validators.targetGroup.nameValidation())`${h(appName)}-${h(
            resourceId
        )}`
        if (params.targetType != 'lambda' && !(params.port && params.protocol)) {
            throw new Error('Port and Protocol must be specified for non lambda target types')
        }
        switch (params.targetType) {
            case 'ip':
                targetGroup = new aws.lb.TargetGroup(`${appName}-${resourceId}-targetGroup`, {
                    name: tgName,
                    port: params.port,
                    protocol: params.protocol,
                    targetType: 'ip',
                    vpcId: params.vpcId,
                    tags: params.tags,
                })
                break
            case 'instance':
                targetGroup = new aws.lb.TargetGroup(`${appName}-${resourceId}-targetGroup`, {
                    name: tgName,
                    port: params.port,
                    protocol: params.protocol,
                    vpcId: params.vpcId,
                    tags: params.tags,
                })
                break
            case 'alb':
                targetGroup = new aws.lb.TargetGroup(`${appName}-${resourceId}-targetGroup`, {
                    name: tgName,
                    targetType: 'alb',
                    port: params.port,
                    protocol: params.protocol,
                    vpcId: params.vpcId,
                    loadBalancingAlgorithmType: params.loadBalancingAlgorithmType,
                    tags: params.tags,
                })
                break
            case 'lambda':
                targetGroup = new aws.lb.TargetGroup(`${appName}-${resourceId}-targetGroup`, {
                    name: tgName,
                    targetType: 'lambda',
                    tags: params.tags,
                })
                break
            default:
                throw new Error('Unsupported target group target type')
        }
        return targetGroup
    }

    public attachTargetGroupToResource = (
        appName: string,
        resourceId: string,
        params: TargetGroupAttachmentArgs
    ): aws.lb.TargetGroupAttachment => {
        return new aws.lb.TargetGroupAttachment(`${appName}-${resourceId}-targetGroupAttachment`, {
            targetGroupArn: params.targetGroupArn,
            targetId: params.targetId,
            port: params.port,
        })
    }
}
