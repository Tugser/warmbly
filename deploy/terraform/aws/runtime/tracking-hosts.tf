variable "tracking_additional_hosts" {
  description = "Tracking hostnames with validated DNS and TLS at the edge"
  type        = set(string)
  default     = []
  validation {
    condition = alltrue([for host in var.tracking_additional_hosts :
      length(host) <= 128 && can(regex("^[a-zA-Z0-9][a-zA-Z0-9.-]*\\.[a-zA-Z]{2,}$", host))
    ])
    error_message = "Provide DNS hostnames supported by an ALB host-header rule."
  }
}

resource "aws_lb_listener_rule" "tracking_alias" {
  for_each = {
    for index, hosts in chunklist(sort(tolist(var.tracking_additional_hosts)), 5) : tostring(index) => hosts
  }
  listener_arn = aws_lb_listener.https.arn
  priority     = 200 + tonumber(each.key)
  condition {
    host_header { values = each.value }
  }
  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.service["tracking"].arn
  }
}
